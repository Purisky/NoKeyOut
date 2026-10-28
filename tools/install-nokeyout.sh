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
OFFLINE=""
MAGPIE_VERSION="${NOKEYOUT_MAGPIE_VERSION:-0.1.1892}"
SCREEN_MSG="${NOKEYOUT_SCREEN_MSG:-}"
LANG_CODE="$(cat /var/lib/nokeyout/lang 2>/dev/null | head -1 || true)"
[ "$LANG_CODE" = "zh" ] || LANG_CODE="en"
TMP=""
screen_msg() { [ -n "$SCREEN_MSG" ] && [ -x "$SCREEN_MSG" ] && "$SCREEN_MSG" screen-msg "$@" >/dev/null 2>&1 || true; }
die() { screen_msg error -1 "$LANG_CODE"; echo "install-nokeyout: $*" >&2; exit 1; }
say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
usage() { cat <<'EOF'
Usage: sudo bash install-nokeyout.sh [options]

  --ip IP             Panel address (auto-detected when omitted)
  --port PORT         Panel HTTPS port (default 8443)
  --version VER       Install a specific release version
  --url URL           Use a release tarball directly
  --sha256 HEX        SHA-256 for --url
  --manifest URL      Alternate manifest URL
  --offline DIR       Install from a local bundle (manifest.json, .sig, release.pub, *.tar.gz)
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
  --offline) OFFLINE="${2:-}"; shift 2 ;;
  --dry-run) DRY=1; shift ;;
  -h|--help) usage; exit 0 ;;
  *) die "unknown argument: $1 (use --help)" ;;
esac; done

[ "$(id -u)" = 0 ] || die "run as root"
for c in openssl tar sed grep awk sha256sum systemctl; do
  command -v "$c" >/dev/null 2>&1 || die "missing tool: $c"
done
if [ -z "$OFFLINE" ]; then
  command -v curl >/dev/null 2>&1 || die "missing tool: curl"
fi

cleanup() { [ -n "$TMP" ] && rm -rf "$TMP"; }
TMP="$(mktemp -d)"; trap cleanup EXIT

fetch() {
  case "$1" in
    file://*) cp "${1#file://}" "$2" ;;
    http://*|https://*) curl -fsSL "$1" -o "$2" ;;
    *) cp "$1" "$2" ;;
  esac
}

if [ -n "$OFFLINE" ]; then
  [ -d "$OFFLINE" ] || die "offline bundle not found: $OFFLINE"
  MANIFEST="$OFFLINE/manifest.json"
  PUBKEY_URL="$OFFLINE/release.pub"
  URL="$OFFLINE/release.tar.gz"
  [ -f "$MANIFEST" ] && [ -f "$MANIFEST.sig" ] && [ -f "$PUBKEY_URL" ] && [ -f "$URL" ] || \
    die "offline bundle incomplete (need manifest.json, manifest.json.sig, release.pub, release.tar.gz)"
fi

ARCH="$(uname -m)"
case "$ARCH" in
  aarch64|arm64) ASSET=linux-arm64 ;;
  x86_64|amd64) ASSET=linux-amd64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

if [ -n "$OFFLINE" ] || [ -z "$URL" ]; then
  screen_msg start 0 "$LANG_CODE"
  if [ -z "$OFFLINE" ]; then
    say "0/4 检查互联网"
    curl -fsS --max-time 8 -o /dev/null https://nokeyout.com/release.pub || \
      die "无法访问 nokeyout.com：检查网线/Wi-Fi、DNS、系统时间（NTP）后重试"
  fi
  say "1/4 校验发布清单"
  fetch "$MANIFEST" "$TMP/manifest.json" || die "cannot read manifest"
  fetch "$MANIFEST.sig" "$TMP/manifest.json.sig" || die "cannot read manifest signature"
  fetch "$PUBKEY_URL" "$TMP/release.pub" || die "cannot read release key"
  openssl pkeyutl -verify -pubin -inkey "$TMP/release.pub" -rawin \
    -in "$TMP/manifest.json" -sigfile "$TMP/manifest.json.sig" >/dev/null 2>&1 \
    || die "manifest signature verification failed"
  VERSION="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -1)"
  MAGPIE_VERSION="$(sed -n 's/.*"magpie"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TMP/manifest.json" | head -1)"
  [ -n "$MAGPIE_VERSION" ] || MAGPIE_VERSION="0.1.1892"
  ASSET_JSON="$(tr -d '\n' < "$TMP/manifest.json" | sed -n "s/.*\"$ASSET\"[[:space:]]*:[[:space:]]*{\([^}]*\)}.*/\1/p")"
  [ -n "$OFFLINE" ] || URL="$(printf '%s' "$ASSET_JSON" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  SHA="$(printf '%s' "$ASSET_JSON" | sed -n 's/.*"sha256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  [ -n "$URL" ] || die "manifest has no $ASSET asset"
  [ -n "$SHA" ] || die "manifest asset has no sha256"
fi

screen_msg verify 10 "$LANG_CODE"
download_with_progress() {
  url="$1"; out="$2"
  total="$(curl -fsIL "$url" 2>/dev/null | awk 'tolower($1)=="content-length:"{v=$2} END{print v}' | tr -d '\r')"
  case "$total" in ''|*[!0-9]*) total=0 ;; esac
  curl -fL --retry 3 --retry-delay 2 -o "$out" "$url" &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$total" -gt 0 ]; then
      n="$(wc -c < "$out" 2>/dev/null || echo 0)"
      pct=$((n * 100 / total)); [ "$pct" -gt 100 ] && pct=100
      screen_msg download $((15 + pct * 40 / 100)) "$LANG_CODE"
    fi
    sleep 1
  done
  wait "$pid"
}

say "2/4 下载 NoKeyOut $VERSION"
TAR="$TMP/$(basename "${URL%%\?*}")"
if [ -n "$OFFLINE" ]; then
  cp "$URL" "$TAR.part" || die "copy bundle failed"
  screen_msg prepare 55 "$LANG_CODE"
else
  download_with_progress "$URL" "$TAR.part" || die "download failed"
  screen_msg download 55 "$LANG_CODE"
fi
mv "$TAR.part" "$TAR"
if [ -n "$SHA" ]; then
  GOT="$(sha256sum "$TAR" | awk '{print $1}')"
  [ "$GOT" = "$SHA" ] || die "sha256 mismatch: got $GOT"
fi

say "3/4 解压 release"
screen_msg extract 60 "$LANG_CODE"
mkdir -p "$TMP/rel"
tar xzf "$TAR" -C "$TMP/rel"
DIR="$(find "$TMP/rel" -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -n "$DIR" ] && [ -f "$DIR/panel/setup.sh" ] || die "release layout invalid (panel/setup.sh missing)"
if [ -z "$SCREEN_MSG" ] && [ -x "$DIR/panel-go/panel" ]; then SCREEN_MSG="$DIR/panel-go/panel"; fi

if [ -z "$IP" ]; then
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
fi
[ -n "$IP" ] || die "cannot detect IP; pass --ip <device-ip>"

if [ "$SKIP_MAGPIE" != 1 ] && ! systemctl is-active --quiet nokeyout-magpie-binary 2>/dev/null; then
  screen_msg gateway 75 "$LANG_CODE"
  say "3b/4 安装 magpie 二进制"
  PULLER="$DIR/tools/magpie-pull/magpie-pull"
  BUNDLED_MAGPIE="$DIR/gateway/bin/magpie"
  if [ -n "$OFFLINE" ] && [ ! -x "$BUNDLED_MAGPIE" ]; then
    die "offline release has no gateway/bin/magpie"
  fi
  [ -x "$PULLER" ] || [ -x "$BUNDLED_MAGPIE" ] || die "release 缺 magpie（gateway/bin/magpie 或 magpie-pull）"
  if [ "$DRY" = 1 ]; then
    if [ -x "$BUNDLED_MAGPIE" ]; then
      echo "  dry-run: bundled $BUNDLED_MAGPIE"
    else
      echo "  dry-run: $PULLER ghcr.io/yetone/magpie:$MAGPIE_VERSION"
    fi
  else
    BIN="/usr/local/bin/magpie-$MAGPIE_VERSION"
    if [ -x "$BUNDLED_MAGPIE" ]; then
      install -m 0755 "$BUNDLED_MAGPIE" "$BIN"
    else
      "$PULLER" "ghcr.io/yetone/magpie:$MAGPIE_VERSION" -o "$BIN" || \
        "$PULLER" "ghcr.nju.edu.cn/yetone/magpie:$MAGPIE_VERSION" -o "$BIN" || \
        die "magpie OCI 直取失败"
    fi
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

screen_msg panel 88 "$LANG_CODE"
say "4/4 安装 NoKeyOut（panel :$PORT，二进制 magpie）"
ARGS=(--ip "$IP" --port "$PORT")
[ "$DRY" = 1 ] && ARGS+=(--dry-run)
( cd "$DIR" && bash panel/setup.sh "${ARGS[@]}" )

screen_msg done 100 "$LANG_CODE"
say "完成"
echo "  管理面: https://$IP:$PORT/"
echo "  屏幕门控: panel-go screen-gate status|pass"
echo "  后续升级: nokeyout-update check && nokeyout-update install"
