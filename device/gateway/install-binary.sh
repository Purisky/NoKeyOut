#!/usr/bin/env bash
#
# 与容器运行完全同参：MAGPIE_ADDR=0.0.0.0:3425、web --addr 127.0.0.1:3430 --no-open、
# 同一份 /config（默认 /home/nokeyout/magpie-config）、同 env（WEB_KEY/NO_STATS）。
# 二进制来源：优先 docker cp（与线上同版本、零下载），失败则官方 release 下载。
#
#   sudo ./install-binary.sh              # 切换（容器停而留作回滚）
#   sudo ./install-binary.sh --dry-run    # 只验证二进制可取、不切换
#
# 回滚：systemctl stop nokeyout-magpie-binary && docker start magpie
set -euo pipefail
CTR="${CTR:-magpie}"
# 配置目录放中立路径（/var/lib 下 65532 可穿越；家目录 750 会阻断非宿主用户的路径解析）
CFG="${NOKEYOUT_CONFIG_DIR:-/var/lib/nokeyout/magpie-config}"
[ -d "$CFG" ] || CFG="/home/nokeyout/magpie-config"
UNIT=/etc/systemd/system/nokeyout-magpie-binary.service
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1
say() { printf '\n== %s ==\n' "$*"; }
die() { echo "install-binary: $*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "root required"

say "1/5 取二进制"
if docker ps --format '{{.Names}}' | grep -qx "$CTR"; then
  MPATH=$(docker exec "$CTR" bash -c 'readlink -f "$(command -v magpie)"' 2>/dev/null | tr -d '\r')
  [ -z "$MPATH" ] && MPATH=/usr/local/bin/magpie
  docker cp "$CTR:$MPATH" /tmp/magpie-new >/dev/null || true
  docker exec "$CTR" magpie --version > /tmp/magpie-ver 2>&1 || true
  VER=$(awk '{print $2}' /tmp/magpie-ver)
  echo "  容器内 $MPATH → $(docker exec "$CTR" magpie --version 2>/dev/null | head -1)"
fi
if [ ! -s /tmp/magpie-new ]; then
  # 容器不可用 → 从官方多架构镜像提取。release 资产只有 CLI、不是服务端二进制，
  say "1b/5 容器不可用，从官方镜像提取"
  IVER="${MAGPIE_IMAGE_VERSION:-0.1.1322}"
  IMG="ghcr.io/yetone/magpie:$IVER"
  systemctl is-active --quiet docker 2>/dev/null || systemctl start docker
  docker pull --quiet "$IMG" >/dev/null 2>&1 || \
    docker pull --quiet "ghcr.nju.edu.cn/yetone/magpie:$IVER" >/dev/null 2>&1 || die "拉取镜像失败"
  CID=$(docker create "$IMG" 2>/dev/null); [ -n "$CID" ] || die "docker create 失败"
  # /usr/local/bin/magpie 是指向 /magpie 的软链，docker cp 不 Dereference
  for p in /magpie /usr/local/bin/magpie /usr/bin/magpie; do
    docker cp "$CID:$p" /tmp/magpie-new >/dev/null 2>&1 && \
      [ "$(stat -c %s /tmp/magpie-new 2>/dev/null || echo 0)" -gt 1048576 ] && break
    rm -f /tmp/magpie-new
  done
  docker rm -f "$CID" >/dev/null 2>&1
  [ -s /tmp/magpie-new ] || die "镜像里找不到 magpie 二进制"
  VER="$IVER"
fi
file /tmp/magpie-new | grep -q ELF || die "不是 ELF 二进制"
chmod 0755 /tmp/magpie-new
[ "$DRY" = 1 ] && { echo "  dry-run 完成（未安装）"; exit 0; }

say "2/7 安装到 /usr/local/bin/magpie-$VER"
install -m 0755 /tmp/magpie-new "/usr/local/bin/magpie-$VER"
ln -sf "/usr/local/bin/magpie-$VER" /usr/local/bin/magpie
# 与容器同 uid（65532）——否则二进制新建的文件容器读不了，回滚即断
getent passwd 65532 >/dev/null || { getent group 65532 >/dev/null || groupadd -r -g 65532 magpie-rt; useradd -r -u 65532 -g 65532 -s /usr/sbin/nologin magpie-rt; }
chown -R 65532:65532 "$CFG" 2>/dev/null || true
rm -f /tmp/magpie-new /tmp/magpie-ver

say "3/7 systemd 单元（与容器同 env/同 /config/同 uid）"
# WEB_KEY：优先面板密钥文件，否则从运行中的容器 env 取（/config 由卷持久化）
WEB_KEY=$(cat /var/lib/nokeyout/secrets/magpie-web.key 2>/dev/null || true)
[ -n "$WEB_KEY" ] || WEB_KEY=$(docker inspect "$CTR" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^MAGPIE_WEB_KEY=//p' | head -1)
DEVICE_IP="${NOKEYOUT_DEVICE_IP:-192.0.2.10}"   # RFC 5737 占位地址；按现场网络覆盖
cat > "$UNIT" <<EOF
[Unit]
Description=NoKeyOut magpie gateway (single binary)
After=network-online.target
[Service]
Type=simple
User=magpie-rt
Group=magpie-rt
Environment=MAGPIE_ADDR=0.0.0.0:3425
Environment=MAGPIE_PUBLIC_URL=http://${DEVICE_IP}:3425
Environment=MAGPIE_WEB_KEY=${WEB_KEY:-}
Environment=MAGPIE_NO_STATS=1
Environment=HOME=$CFG/home
Environment=XDG_CONFIG_HOME=$CFG
Environment=XDG_CACHE_HOME=$CFG/cache
ExecStart=/usr/local/bin/magpie-${VER} web --addr 127.0.0.1:3430 --no-open
Restart=on-failure
RestartSec=2
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable nokeyout-magpie-binary >/dev/null

say "4/5 切换（容器停而留作回滚）"
docker stop "$CTR" >/dev/null 2>&1 || true
systemctl restart nokeyout-magpie-binary

say "5/5 冒烟（healthcheck + 双视角 /v1/models + 真实调用）"
OK=1
LAN_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
for i in $(seq 1 15); do "/usr/local/bin/magpie-$VER" healthcheck >/dev/null 2>&1 && break; sleep 2; done
"/usr/local/bin/magpie-$VER" healthcheck >/dev/null 2>&1 || { echo "  ❌ 网关 healthcheck 失败"; OK=0; }
# 二进制直跑时 loopback 免鉴权（200=网关活着）；LAN 视角必须 401（作用域执行在）
[ "$(curl -s -o /dev/null -m 10 -w '%{http_code}' http://127.0.0.1:3425/v1/models)" = 200 ] || { echo "  ❌ loopback /v1/models 应 200"; OK=0; }
[ -n "$LAN_IP" ] && [ "$(curl -s -o /dev/null -m 10 -w '%{http_code}' http://$LAN_IP:3425/v1/models)" = 401 ] || { echo "  ❌ LAN /v1/models 应 401"; OK=0; }
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
  echo "  ↩ 冒烟失败，回滚到容器"
  systemctl stop nokeyout-magpie-binary
  docker start "$CTR" >/dev/null 2>&1 || {
    echo "  旧容器卷路径已迁移，重建容器…"
    docker rm -f "$CTR" >/dev/null 2>&1 || true
    docker run -d --name "$CTR" --restart unless-stopped -p 3425:3425 -p 127.0.0.1:3430:3430 \
      -e MAGPIE_ADDR=0.0.0.0:3425 -e MAGPIE_PUBLIC_URL=http://${DEVICE_IP}:3425 \
      -e MAGPIE_WEB_KEY="${WEB_KEY:-}" -e MAGPIE_NO_STATS=1 \
      -e HOME="$CFG/home" -e XDG_CONFIG_HOME="$CFG" -e XDG_CACHE_HOME="$CFG/cache" \
      -v "$CFG:/config" ghcr.io/yetone/magpie:"${VER#v}" web --addr 0.0.0.0:3430 --no-open >/dev/null
  }
  die "已回滚到容器模式"
fi
echo
echo "✅ magpie 现以单二进制运行（$VER）。回滚: systemctl stop nokeyout-magpie-binary && docker start $CTR"
