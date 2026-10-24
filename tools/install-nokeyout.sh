#!/usr/bin/env bash
# install-nokeyout.sh —— 在“已经装好 Linux 的设备”上在线安装 NoKeyOut
#
#   curl -fsSL https://nokeyout.com/tools/install-nokeyout.sh -o /tmp/install-nokeyout.sh
#   sudo bash /tmp/install-nokeyout.sh
#
# 脚本会：校验签名 manifest → 下载 release → 校验 sha256 → 解压 →
# 安装 magpie 二进制（OCI 直取）→ 调用 release 内的 panel/setup.sh。
set -euo pipefail

MANIFEST="${NOKEYOUT_RELEASE_URL:-https://nokeyout.com/releases/stable/manifest.json}"
PUBKEY_URL="${NOKEYOUT_PUBKEY_URL:-https://nokeyout.com/release.pub}"
URL=""
SHA=""
VERSION=""
IP=""
PORT="8443"
DRY=0
SKIP_MAGPIE=0
MAGPIE_VERSION="${NOKEYOUT_MAGPIE_VERSION:-0.1.1322}"
TMP=""
die() { echo "install-nokeyout: $*" >&2; exit 1; }
say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
usage() { cat <<'EOF'
Usage: sudo bash install-nokeyout.sh [options]

  --ip IP             Panel address (auto-detected when omitted)
  --port PORT         Panel HTTPS port (default 8443)
  --version VER       Install a specific release version
  --url URL           Use a release tarball directly
  --sha256 HEX        SHA-256 for --url
  --manifest URL      Alternate manifest URL
  --skip-magpie-check Do not install magpie; use the existing service
  --dry-run           Print actions without changing the device
  -h, --help          Show this help
EOF
}

while [ $# -gt 0 ]; do case "$1" in
  --url) URL="${2:-}"; shift 2 ;;
  --sha256) SHA="${2:-}"; shift 2 ;;
  --version) VERSION="${2:-}"; shift 2 ;;
  --ip) IP="${2:-}"; shift 2 ;;
  --port) PORT="${2:-}"; shift 2 ;;
  --skip-magpie-check) SKIP_MAGPIE=1; shift ;;
  --manifest) MANIFEST="${2:-}"; shift 2 ;;
  --dry-run) DRY=1; shift ;;
  -h|--help) usage; exit 0 ;;
  *) die "unknown argument: $1 (use --help)" ;;
esac; done

[ "$(id -u)" = 0 ] || die "run as root"
for c in curl openssl tar sed grep awk sha256sum systemctl; do
  command -v "$c" >/dev/null 2>&1 || die "missing tool: $c"
done

cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
TMP="$(mktemp -d)"; trap cleanup EXIT

ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) ASSET=linux-arm64 ;;
  x86_64|amd64) ASSET=linux-amd64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

if [ -z "$URL" ]; then
  say "0/4 检查互联网"
  curl -fsS --max-time 8 -o /dev/null https://nokeyout.com/release.pub || \
    die "无法访问 nokeyout.com：检查网线/Wi-Fi、DNS、系统时间（NTP）后重试"
  say "1/4 校验发布清单"
  curl -fsSL "$MANIFEST" -o "$TMP/manifest.json" || die "cannot fetch manifest"
  curl -fsSL "$MANIFEST.sig" -o "$TMP/manifest.json.sig" || die "cannot fetch manifest signature"
  curl -fsSL "$PUBKEY_URL" -o "$TMP/release.pub" || die "cannot fetch release key"
  openssl pkeyutl -verify -pubin -inkey "$TMP/release.pub" -rawin \
    -in "$TMP/manifest.json" -sigfile "$TMP/manifest.json.sig" >/dev/null 2>&1 \
    || die "manifest signature verification failed"
  VERSION="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -1)"
  MAGPIE_VERSION="$(sed -n 's/.*"magpie"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -1)"
  [ -n "$MAGPIE_VERSION" ] || MAGPIE_VERSION="0.1.1322"
  ASSET_JSON="$(tr -d '\n' < "$TMP/manifest.json" | sed -n "s/.*\"$ASSET\"[[:space:]]*:[[:space:]]*{\([^}]*\)}.*/\1/p")"
  URL="$(printf '%s' "$ASSET_JSON" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  SHA="$(printf '%s' "$ASSET_JSON" | sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ -n "$URL" ] || die "manifest has no $ASSET asset"
  [ -n "$SHA" ] || die "manifest asset has no sha256"
fi

say "2/4 下载 NoKeyOut $VERSION"
TAR="$TMP/$(basename "${URL%%\?*}")"
curl -fL --retry 3 --retry-delay 2 -o "$TAR.part" "$URL" || die "download failed"
mv "$TAR.part" "$TAR"
if [ -n "$SHA" ]; then
  GOT="$(sha256sum "$TAR" | awk '{print $1}')"
  [ "$GOT" = "$SHA" ] || die "sha256 mismatch: got $GOT"
fi

say "3/4 解压 release"
mkdir -p "$TMP/rel"
tar xzf "$TAR" -C "$TMP/rel"
DIR="$(find "$TMP/rel" -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -n "$DIR" ] && [ -f "$DIR/panel/setup.sh" ] || die "release layout invalid (panel/setup.sh missing)"

if [ -z "$IP" ]; then
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi
[ -n "$IP" ] || die "cannot detect IP; pass --ip <device-ip>"

if [ "$SKIP_MAGPIE" != 1 ] && ! systemctl is-active --quiet nokeyout-magpie-binary 2>/dev/null; then
  say "3b/4 安装 magpie 二进制（OCI 直取）"
  PULLER="$DIR/tools/magpie-pull/magpie-pull"
  [ -x "$PULLER" ] || die "release 缺 tools/magpie-pull/magpie-pull；无法安装 magpie"
  if [ "$DRY" = 1 ]; then
    echo "  dry-run: $PULLER ghcr.io/yetone/magpie:$MAGPIE_VERSION"
  else
    BIN="/usr/local/bin/magpie-$MAGPIE_VERSION"
    "$PULLER" "ghcr.io/yetone/magpie:$MAGPIE_VERSION" -o "$BIN" || \
      "$PULLER" "ghcr.nju.edu.cn/yetone/magpie:$MAGPIE_VERSION" -o "$BIN" || \
      die "magpie OCI 直取失败"
    chmod 0755 "$BIN"; ln -sfn "$BIN" /usr/local/bin/magpie
    getent group 65532 >/dev/null || groupadd -r -g 65532 magpie-rt
    getent passwd 65532 >/dev/null || useradd -r -u 65532 -g 65532 -s /usr/sbin/nologin magpie-rt
    CFG=/var/lib/nokeyout/magpie-config
    install -d -m 0755 "$CFG" "$CFG/home" "$CFG/cache"
    install -d -m 0700 /var/lib/nokeyout/secrets
    if [ ! -s /var/lib/nokeyout/secrets/magpie-web.key ]; then
      openssl rand -hex 16 > /var/lib/nokeyout/secrets/magpie-web.key
      chmod 600 /var/lib/nokeyout/secrets/magpie-web.key
    fi
    WEBKEY="$(cat /var/lib/nokeyout/secrets/magpie-web.key)"
    chown -R 65532:65532 "$CFG" 2>/dev/null || true
    cat > /etc/systemd/system/nokeyout-magpie-binary.service <<EOS
[Unit]
Description=NoKeyOut magpie gateway (single binary)
After=network-online.target
[Service]
Type=simple
User=magpie-rt
Group=magpie-rt
Environment=MAGPIE_ADDR=0.0.0.0:3425
Environment=MAGPIE_PUBLIC_URL=http://$IP:3425
Environment=MAGPIE_WEB_KEY=$WEBKEY
Environment=MAGPIE_NO_STATS=1
Environment=HOME=$CFG/home
Environment=XDG_CONFIG_HOME=$CFG
Environment=XDG_CACHE_HOME=$CFG/cache
ExecStart=$BIN web --addr 127.0.0.1:3430 --no-open
Restart=on-failure
RestartSec=2
[Install]
WantedBy=multi-user.target
EOS
    systemctl daemon-reload
    systemctl enable --now nokeyout-magpie-binary
    for i in $(seq 1 20); do "$BIN" healthcheck >/dev/null 2>&1 && break; sleep 1; done
    "$BIN" healthcheck >/dev/null 2>&1 || die "magpie healthcheck 失败"
  fi
fi

say "4/4 安装 NoKeyOut（panel :$PORT，二进制 magpie）"
ARGS=(--ip "$IP" --port "$PORT")
[ "$DRY" = 1 ] && ARGS+=(--dry-run)
( cd "$DIR" && bash panel/setup.sh "${ARGS[@]}" )

say "完成"
echo "  管理面: https://$IP:$PORT/"
echo "  屏幕门控: panel-go screen-gate status|pass"
echo "  后续升级: nokeyout-update check && nokeyout-update install"
