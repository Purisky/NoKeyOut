#!/usr/bin/env bash
# release.sh — 打包 NoKeyOut 设备侧发布物（在开发机/仓库根运行）
#
#   ./device/release.sh            # 版本号取 git describe，失败取日期
#   ./device/release.sh 0.2.0      # 指定版本
#
# 内部结构保持 device/ 的目录层级（panel/ + gateway/ 兄弟目录——setup.sh 依赖它）。
#
# 产出 nokeyout-<ver>-linux-<arch>.tar.gz（含 VERSION、各 root helper），然后：
#   scp nokeyout-<ver>-linux-arm64.tar.gz <user>@<device>:/tmp/
#   ssh <user>@<device> 'sudo /usr/local/bin/nokeyout-upgrade nk-deploy nokeyout-<ver>-linux-arm64.tar.gz'
#
# 设备侧 nk-deploy：备份 → 覆盖 panel/helpers → 重跑 setup.sh（幂等）→ 重启 →
# 冒烟（面板 active + healthz + 网关 healthcheck）→ 失败自动回滚。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VER="${1:-}"
ARCH="${ARCH:-arm64}"
if [ -z "$VER" ]; then
  VER=$(git describe --tags --always --dirty 2>/dev/null || true)
  [ -n "$VER" ] || VER="nightly-$(date +%Y%m%d)"
fi
STAGE=$(mktemp -d); trap 'rm -rf "$STAGE"' EXIT
D="$STAGE/nokeyout-$VER"; mkdir -p "$D/panel/web" "$D/gateway/luks" "$D/scripts"

# 面板：整棵树（新增文件如 nokeyout-cert.sh 自动入选）
# 只发 shell/ps1/web，实现全部是 Go 静态二进制。
for f in device/panel/*.sh device/panel/*.ps1; do
  [ -e "$f" ] && cp "$f" "$D/panel/"
done
# 开关门控：两个无扩展名的 helper + systemd 单元 + 设备树上拉片段
cp device/panel/nokeyout-switchd device/panel/nokeyout-gate-check "$D/panel/" || {
  echo "  ❌ 包内缺门控 helper（nokeyout-switchd / nokeyout-gate-check）" >&2
  exit 1
}
mkdir -p "$D/panel/systemd/ssh.service.d" "$D/panel/overlays"
cp device/panel/systemd/*.service "$D/panel/systemd/"
cp device/panel/systemd/ssh.service.d/*.conf "$D/panel/systemd/ssh.service.d/"
cp device/panel/overlays/nokeyout-gate.dts "$D/panel/overlays/"
for f in device/panel/web/*; do
  case "$f" in */__pycache__) continue;; esac
  [ -f "$f" ] && cp "$f" "$D/panel/web/"
done
cp device/gateway/nokeyout-auth \
   device/gateway/nokeyout-magpie-cli device/gateway/nokeyout-upgrade \
   device/gateway/update-magpie.sh device/gateway/update-magpie-binary.sh \
   device/gateway/install-binary.sh device/gateway/magpie-compose.yml "$D/gateway/"
cp device/gateway/luks/setup-luks-volume.sh "$D/gateway/luks/"
mkdir -p "$D/panel-go" "$D/update" "$D/gateway/bin" "$D/tools/magpie-pull"
if [ ! -f device/tools/magpie-pull/magpie-pull ]; then
  echo "  building tools/magpie-pull for $ARCH ..."
  ( cd device/tools/magpie-pull && GOARCH="$ARCH" ./build.sh ) || {
    echo "  ❌ 无法构建 tools/magpie-pull；先装 Go 或预先构建" >&2
    exit 1
  }
fi
cp device/tools/magpie-pull/magpie-pull "$D/tools/magpie-pull/" || exit 1
# 离线首装需要 arm64 magpie 直接随包；用主机架构的 puller 拉取目标架构镜像。
MAGPIE_VER="${NOKEYOUT_MAGPIE_VERSION:-0.1.2270}"
HOST_PULLER="$STAGE/magpie-pull-host"
if [ -n "${NOKEYOUT_MAGPIE_BIN:-}" ] && [ -f "$NOKEYOUT_MAGPIE_BIN" ]; then
  cp "$NOKEYOUT_MAGPIE_BIN" "$D/gateway/bin/magpie"
elif [ ! -x "$D/gateway/bin/magpie" ]; then
  ( cd device/tools/magpie-pull && go build -o "$HOST_PULLER" . ) || {
    echo "  ❌ 无法构建主机 magpie-pull（离线包需要）" >&2
    exit 1
  }
  "$HOST_PULLER" -arch arm64 "ghcr.io/yetone/magpie:$MAGPIE_VER" -o "$D/gateway/bin/magpie" || {
    echo "  ❌ 无法拉取 arm64 magpie $MAGPIE_VER（离线包需要）" >&2
    exit 1
  }
fi
chmod 0755 "$D/gateway/bin/magpie"
cp device/gateway/nokeyout-auth "$D/gateway/bin/" 2>/dev/null || \
  echo "  ⚠️ 包内无 gateway/bin/nokeyout-auth（发布前先跑 device/gateway/build-auth.sh）"
cp device/panel-go/panel "$D/panel-go/" 2>/dev/null || {
  echo "  ❌ 包内无 panel-go/panel；先跑 device/panel-go/build.sh" >&2
  exit 1
}
cp device/update/nokeyout-update "$D/update/" 2>/dev/null || \
  echo "  ⚠️ 包内无 update/nokeyout-update（发布前先跑 device/update/build.sh）"
[ -f device/update/release.pub ] && cp device/update/release.pub "$D/update/"
cp device/scripts/verify-magpie.sh "$D/scripts/"
# Windows 工作区可能把 .sh 写成 CRLF；tarball 里的 shell 必须是 LF，
# 否则设备上 bash 会把 `pipefail\r` 当成无效选项（setup.sh 直接失败）。
for f in "$D"/panel/*.sh "$D"/panel/nokeyout-switchd "$D"/panel/nokeyout-gate-check \
         "$D"/gateway/*.sh "$D"/gateway/nokeyout-upgrade \
         "$D"/gateway/nokeyout-magpie-cli "$D"/gateway/luks/*.sh "$D"/scripts/*.sh; do
  [ -f "$f" ] && sed -i 's/\r$//' "$f"
done

printf '%s\n' "$VER" > "$D/VERSION"

OUT="nokeyout-$VER-linux-$ARCH.tar.gz"
tar czf "$OUT" -C "$STAGE" "nokeyout-$VER"
cat <<EOF

✅ 发布物: $OUT  ($(du -h "$OUT" | cut -f1))
   sha256: $(sha256sum "$OUT" | cut -d' ' -f1)

部署（设备上）:
  scp $OUT <user>@<device-ip>:/tmp/
  ssh <user>@<device-ip> 'sudo /usr/local/bin/nokeyout-upgrade nk-deploy $OUT'

回滚（设备上）:
  ssh <user>@<device-ip> 'sudo /usr/local/bin/nokeyout-upgrade nk-rollback'
EOF
