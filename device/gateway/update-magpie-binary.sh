#!/usr/bin/env bash
#
#   sudo ./update-magpie-binary.sh 0.1.1892 [--arch aarch64]
set -euo pipefail
NEWVER="${1:-}"; shift || true
ARCH="${ARCH:-$(uname -m)}"
say() { printf '\n== %s ==\n' "$*"; }
die() { echo "update-magpie-binary: $*" >&2; exit 1; }
# 设备镜像不一定有 file(1)（Armbian/Buildroot 都可能没有）；用 od 读魔数。
is_elf() { [ "$(head -c 4 "$1" 2>/dev/null | od -An -tx1 | tr -d ' \n')" = "7f454c46" ]; }
[ "$(id -u)" = 0 ] || die "root required"
[ -n "$NEWVER" ] || die "用法: update-magpie-binary.sh <x.y.z>"
[[ "$NEWVER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "版本号格式 x.y.z"
UNIT=/etc/systemd/system/nokeyout-magpie-binary.service
[ -f "$UNIT" ] || die "不是二进制模式（无 $UNIT）"
# 命名约定从 unit 的 ExecStart 反推（magpie-<前缀><版本>，前缀可能是 v 或空），
UNITBIN=$(sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$UNIT")
[ -n "$UNITBIN" ] || die "ExecStart 解析失败"
CUR=$(echo "$UNITBIN" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
[ -n "$CUR" ] || die "无法从 $UNITBIN 解析当前版本"
BN=$(basename "$UNITBIN")
PREFIX=$(echo "${BN#magpie-}" | sed "s/$CUR\$//")   # "v" 或 ""
NEWBIN="$(dirname "$UNITBIN")/magpie-${PREFIX}${NEWVER}"
CURBIN="$UNITBIN"
# LAN IP：冒烟验 401 必须走 LAN（loopback 127.0.0.1 免鉴权，恒 200）
LANIP=$(sed -n 's/.*MAGPIE_PUBLIC_URL=http:\/\/\([0-9.]*\).*/\1/p' "$UNIT")
[ -n "$LANIP" ] || LANIP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "$LANIP" ] || LANIP=127.0.0.1

say "1/5 获取 $ARCH $NEWVER（镜像提取）"
# 预置文件优先：每日任务/离线部署把二进制放到 /tmp/magpie-<版本> 后直接使用，
# 设备不必能访问 ghcr。MAGPIE_SRC 可指向别处（仅 root 本地路径）。
STAGED="${MAGPIE_SRC:-/tmp/magpie-$NEWVER}"
if [ -s "$STAGED" ] && is_elf "$STAGED"; then
  echo "  pre-staged $STAGED"
  [ "$STAGED" = "/tmp/magpie-$NEWVER" ] || cp "$STAGED" "/tmp/magpie-$NEWVER"
else
  # assets 全是 magpie-cli-*）。两种架构统一从官方多架构镜像提取。
  IMG="ghcr.io/yetone/magpie:$NEWVER"
  PULLER="${NOKEYOUT_PULLER:-/usr/local/lib/nokeyout/bin/magpie-pull}"
  if [ -x "$PULLER" ]; then
    # 首选：静态 OCI 直取器（Buildroot 目标同可用）
    echo "  magpie-pull $IMG"
    "$PULLER" "$IMG" -o "/tmp/magpie-$NEWVER" || "$PULLER" "ghcr.nju.edu.cn/yetone/magpie:$NEWVER" -o "/tmp/magpie-$NEWVER" || \
      die "OCI 直取失败"
  else
    # 回退：docker 拉起守护进程提二进制（arm64 与 docker 分叉均测过）
    systemctl is-active --quiet docker 2>/dev/null || systemctl start docker
    echo "  docker pull $IMG"
    docker pull --quiet "$IMG" >/dev/null 2>&1 || \
      docker pull --quiet "ghcr.nju.edu.cn/yetone/magpie:$NEWVER" >/dev/null 2>&1 || \
      die "拉取镜像失败（ghcr denied 时用 ghcr.nju.edu.cn；或先 docker pull 好再跑）"
    CID=$(docker create "$IMG" 2>/dev/null)
    [ -n "$CID" ] || die "docker create 失败"
    # 镜像里 /usr/local/bin/magpie 是指向 /magpie 的软链；docker cp 不 Dereference，
    # 必须拷真实文件（用体积 >1MB 过滤软链）
    for p in /magpie /usr/local/bin/magpie /usr/bin/magpie /app/magpie; do
      if docker cp "$CID:$p" "/tmp/magpie-$NEWVER" 2>/dev/null && \
         [ "$(stat -c %s "/tmp/magpie-$NEWVER" 2>/dev/null || echo 0)" -gt 1048576 ]; then
        break
      fi
      rm -f "/tmp/magpie-$NEWVER"
    done
    [ -f "/tmp/magpie-$NEWVER" ] || { docker rm -f "$CID" >/dev/null 2>&1; die "镜像里找不到 magpie 二进制"; }
    docker rm -f "$CID" >/dev/null 2>&1
  fi
fi
is_elf "/tmp/magpie-$NEWVER" || die "不是 ELF"
chmod 0755 "/tmp/magpie-$NEWVER"
install -m 0755 "/tmp/magpie-$NEWVER" "$NEWBIN"
rm -f "/tmp/magpie-$NEWVER"


say "2/5 切换 unit 指向 $NEWVER"
sed -i "s|$(basename "$CURBIN")|$(basename "$NEWBIN")|" "$UNIT"
[ "$(grep -c "$(basename "$NEWBIN")" "$UNIT")" = 1 ] || die "unit 切换失败"
ln -sfn "$NEWBIN" /usr/local/bin/magpie
systemctl daemon-reload
systemctl restart nokeyout-magpie-binary

say "3/5 冒烟（healthcheck + /v1/models + 一次真实调用）"
OK=1
for i in $(seq 1 15); do "$NEWBIN" healthcheck >/dev/null 2>&1 && break; sleep 2; done
"$NEWBIN" healthcheck >/dev/null 2>&1 || { echo "  ❌ healthcheck"; OK=0; }
# 端口起来也要时间：401（鉴权拒绝=服务在听）轮询等就绪
for i in $(seq 1 10); do
  [ "$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://$LANIP:3425/v1/models)" = 401 ] && break
  sleep 1
done
[ "$(curl -s -o /dev/null -m 5 -w '%{http_code}' http://$LANIP:3425/v1/models)" = 401 ] || { echo "  ❌ /v1Models (LAN $LANIP)"; OK=0; }
KEY=$(cat /var/lib/nokeyout/secrets/panel-gateway.key 2>/dev/null || true)
if [ "$OK" = 1 ] && [ -n "$KEY" ]; then
  MODEL=$(curl -s -m 10 -H "Authorization: Bearer $KEY" http://127.0.0.1:3425/v1/models | grep -oE '"id":"[^"]+"' | head -1 | cut -d'"' -f4 || true)
  if [ -n "$MODEL" ]; then
    C=$(curl -s -o /dev/null -m 60 -w '%{http_code}' -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
      -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_tokens\":4}" \
      http://127.0.0.1:3425/v1/chat/completions)
    [ "$C" = 200 ] || { echo "  ❌ 真实调用 $C"; OK=0; }
    [ "$C" = 200 ] && echo "  ✅ 真实调用 200（$MODEL）"
  fi
fi

if [ "$OK" != 1 ]; then
  say "↩ 冒烟失败，回滚到 $CUR"
  sed -i "s|$(basename "$NEWBIN")|$(basename "$CURBIN")|" "$UNIT"
  ln -sfn "$CURBIN" /usr/local/bin/magpie
  systemctl daemon-reload
  systemctl restart nokeyout-magpie-binary
  die "已回滚到 $CUR"
fi

say "4/5 记录锁版"
printf '%s\n' "$NEWVER" > /var/lib/nokeyout/last-good-version

say "5/5 完成 ✅ $CUR → $NEWVER（旧版保留，可随时回滚）"
