#!/usr/bin/env bash
# install-tf.sh —— 按板型一键：官方 Armbian 镜像 → 写卡 → firstboot 部署 NoKeyOut
#
#   sudo bash tools/install-tf.sh --board neo3plus
#   bash tools/install-tf.sh --board neo3plus --resolve-only   # 只解析镜像
#
# 板型表：tools/boards.json。多个板型可以共用一个 image key（同一个基础镜像）。
# 不维护自建整盘镜像；首次启动自动运行 install-nokeyout.sh。
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BOARD=""
IMAGE_URL=""
SHA=""
DEPLOY=1
RESOLVE_ONLY=0
INDEX="https://github.armbian.com/armbian-images.json"
TOOLS_BASE="${NOKEYOUT_TOOLS_BASE:-https://nokeyout.com/tools}"
CACHE_BASE="${NOKEYOUT_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}}"
RELEASE_MANIFEST="${NOKEYOUT_RELEASE_URL:-https://nokeyout.com/releases/stable/manifest.json}"
RELEASE_PUBKEY="${NOKEYOUT_PUBKEY_URL:-https://nokeyout.com/release.pub}"
die() { echo "install-tf: $*" >&2; exit 1; }
say() { printf '\n\033[1m== %s \033[0m\n' "$*"; }
usage() { cat <<'EOF'
Usage: sudo bash tools/install-tf.sh --board NAME [options]

  --board NAME       Board id from tools/boards.json (e.g. neo3plus)
  --image-url URL    Skip Armbian resolution; flash this image
  --sha256 HEX       Optional SHA-256 for --image-url
  --no-deploy        Flash only; do not inject the first-boot installer
  --resolve-only     Print the resolved image and exit (no root needed)
  -h, --help         Show this help
EOF
}

case "${LC_ALL:-${LANG:-}}" in
  zh*) LANG_CODE=zh ;;
  *) LANG_CODE=en ;;
esac
[ "${NOKEYOUT_LANG:-}" = "zh" ] && LANG_CODE=zh

while [ $# -gt 0 ]; do case "$1" in
  --board) BOARD="${2:-}"; shift 2 ;;
  --image-url) IMAGE_URL="${2:-}"; shift 2 ;;
  --sha256) SHA="${2:-}"; shift 2 ;;
  --no-deploy) DEPLOY=0; shift ;;
  --resolve-only) RESOLVE_ONLY=1; shift ;;
  -h|--help) usage; exit 0 ;;
  *) die "unknown argument: $1 (use --help)" ;;
esac; done
[ "$RESOLVE_ONLY" = 1 ] || [ "$(id -u)" -eq 0 ] || die "run as root"
[ -n "$BOARD" ] || die "--board is required (see tools/boards.json)"
PYTHON=""
for c in python3 python; do
  if command -v "$c" >/dev/null 2>&1 && "$c" -c "import sys; assert sys.version_info[0] >= 3" >/dev/null 2>&1; then PYTHON="$c"; break; fi
done
[ -n "$PYTHON" ] || die "python3 is required to resolve the Armbian image index"
command -v curl >/dev/null 2>&1 || die "curl is required"
for c in openssl tar sha256sum; do command -v "$c" >/dev/null 2>&1 || die "$c is required"; done

say "1/3 解析板型与官方镜像"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
BOARDS="$HERE/boards.json"
if [ ! -f "$BOARDS" ]; then
  BOARDS="$TMP/boards.json"
  curl -fsSL "$TOOLS_BASE/boards.json" -o "$BOARDS" || die "cannot fetch boards.json"
fi
if [ -z "$IMAGE_URL" ]; then
  curl -fsSL "$INDEX" -o "$TMP/armbian.json" || die "cannot fetch Armbian image index"
  RESOLVED="$($PYTHON - "$BOARDS" "$TMP/armbian.json" "$BOARD" <<'PY'
import json, sys
from re import sub
boards_path, index_path, board = sys.argv[1:4]
reg = json.load(open(boards_path, encoding="utf-8"))
entry = reg.get("boards", {}).get(board)
if not entry:
    sys.exit("unknown board: " + board)
imgkey = entry.get("image")
img = reg.get("images", {}).get(imgkey)
if not img:
    sys.exit("unknown image key: " + str(imgkey))
slug = img.get("armbian") or board
want_variant = img.get("variant") or "minimal"
want_branch = img.get("branch") or "current"
assets = [a for a in json.load(open(index_path, encoding="utf-8")).get("assets", [])
          if a.get("board_slug") == slug]
if not assets:
    sys.exit("Armbian has no image for board_slug=" + slug)

def vkey(v):
    return tuple(int(x) for x in sub(r"[^0-9.].*$", "", v).split(".") if x.isdigit())
def rank(a):
    return (
        0 if a.get("variant") == want_variant else 1,
        0 if a.get("branch") == want_branch else 1,
        a.get("variant") == "minimal",
        0 if a.get("board_support") == "conf" else 1,
        vkey(a.get("armbian_version") or "0"),
        a.get("file_date") or "",
    )
best = max(assets, key=rank)
print("\t".join([best.get("file_url",""), best.get("file_url_sha",""),
                 best.get("armbian_version",""), best.get("variant",""),
                 best.get("board_support",""), best.get("board_name","")]))
PY
)" || die "image resolution failed"
  IFS=$'\t' read -r IMAGE_URL SHA_URL VERSION VARIANT SUPPORT BOARD_NAME <<< "$RESOLVED"
  [ -n "$IMAGE_URL" ] || die "resolved empty image URL"
  if [ -z "$SHA" ] && [ -n "$SHA_URL" ]; then
    SHA="$(curl -fsSL "$SHA_URL" 2>/dev/null | head -c 200 | awk '{print $1}' || true)"
    [ -n "$SHA" ] || echo "  ⚠️ 该镜像没有 .sha 文件，跳过校验"
  fi
  echo "  board:   $BOARD ($BOARD_NAME)"
  echo "  image:   $IMAGE_URL"
  echo "  version: $VERSION · $VARIANT · support=$SUPPORT"
  echo "  sha256:  $SHA"
else
  [ -n "$SHA" ] || echo "  ⚠️ 未提供 --sha256，跳过镜像校验"
fi
[ -n "$IMAGE_URL" ] || die "empty image URL"
[ "$RESOLVE_ONLY" = 1 ] && exit 0

FB=""
BUNDLE=""
if [ "$DEPLOY" = 1 ]; then
  say "2/3 准备离线安装包"
  BUNDLE="$TMP/bundle"
  mkdir -p "$BUNDLE/extract"
  curl -fsSL "$RELEASE_MANIFEST" -o "$BUNDLE/manifest.json" || die "cannot fetch release manifest"
  curl -fsSL "$RELEASE_MANIFEST.sig" -o "$BUNDLE/manifest.json.sig" || die "cannot fetch manifest signature"
  curl -fsSL "$RELEASE_PUBKEY" -o "$BUNDLE/release.pub" || die "cannot fetch release key"
  openssl pkeyutl -verify -pubin -inkey "$BUNDLE/release.pub" -rawin \
    -in "$BUNDLE/manifest.json" -sigfile "$BUNDLE/manifest.json.sig" >/dev/null 2>&1 \
    || die "release manifest signature verification failed"
  IFS=$'\t' read -r RVER RMAGPIE RURL RSHA <<< "$($PYTHON - "$BUNDLE/manifest.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1], encoding="utf-8"))
a = (m.get("assets") or {}).get("linux-arm64") or {}
print("\t".join([str(m.get("version", "")), str(m.get("magpie", "")),
                 str(a.get("url", "")), str(a.get("sha256", ""))]))
PY
)"
  [ -n "$RURL" ] && [ -n "$RSHA" ] || die "manifest has no linux-arm64 asset"
  say "  release: $RVER (magpie $RMAGPIE)"
  REL_CACHE="$CACHE_BASE/nokeyout-releases"
  mkdir -p "$REL_CACHE"
  CACHED="$REL_CACHE/$(basename "${RURL%%\?*}")"
  if [ -s "$CACHED" ] && echo "$RSHA  $CACHED" | sha256sum -c - >/dev/null 2>&1; then
    say "  using cached $(basename "$CACHED")"
  else
    [ -s "$CACHED" ] && rm -f "$CACHED"
    if [ -s "$CACHED.part" ]; then
      say "  resuming $(basename "$CACHED")"
      curl -fL --retry 3 --retry-delay 3 -C - -o "$CACHED.part" "$RURL" || \
        curl -fL --retry 3 --retry-delay 3 -o "$CACHED.part" "$RURL" || die "cannot download release"
    else
      curl -fL --retry 3 --retry-delay 3 -o "$CACHED.part" "$RURL" || die "cannot download release"
    fi
    echo "$RSHA  $CACHED.part" | sha256sum -c - >/dev/null || {
      rm -f "$CACHED.part"
      die "release sha256 mismatch"
    }
    mv "$CACHED.part" "$CACHED"
  fi
  ln -f "$CACHED" "$BUNDLE/release.tar.gz" 2>/dev/null || cp "$CACHED" "$BUNDLE/release.tar.gz"
  tar xzf "$BUNDLE/release.tar.gz" -C "$BUNDLE/extract"
  PANEL="$(find "$BUNDLE/extract" -path '*/panel-go/panel' -type f | head -1)"
  MAGPIE_BIN="$(find "$BUNDLE/extract" -path '*/gateway/bin/magpie' -type f | head -1)"
  [ -n "$PANEL" ] || die "release has no panel-go/panel"
  [ -n "$MAGPIE_BIN" ] || die "release has no gateway/bin/magpie; rebuild it for offline first boot"
  cp "$PANEL" "$BUNDLE/panel"; chmod 0755 "$BUNDLE/panel"
  rm -rf "$BUNDLE/extract"
  if [ -f "$HERE/install-nokeyout.sh" ]; then
    cp "$HERE/install-nokeyout.sh" "$BUNDLE/install-nokeyout.sh"
  else
    curl -fsSL "$TOOLS_BASE/install-nokeyout.sh" -o "$BUNDLE/install-nokeyout.sh" || die "cannot fetch install-nokeyout.sh"
  fi
  chmod 0755 "$BUNDLE/install-nokeyout.sh"
  if [ -f "$HERE/../device/panel/web/firstboot.html" ]; then
    cp "$HERE/../device/panel/web/firstboot.html" "$BUNDLE/firstboot.html"
  else
    curl -fsSL "$TOOLS_BASE/firstboot.html" -o "$BUNDLE/firstboot.html" || true
  fi
  if [ -f "$HERE/nokeyout-screen.dtbo" ]; then
    cp "$HERE/nokeyout-screen.dtbo" "$BUNDLE/nokeyout-screen.dtbo"
  else
    curl -fsSL "$TOOLS_BASE/nokeyout-screen.dtbo" -o "$BUNDLE/nokeyout-screen.dtbo" || die "cannot fetch nokeyout-screen.dtbo"
  fi

  FB="$TMP/nokeyout-firstboot.sh"
  cat > "$FB" <<'EOS'
#!/bin/sh
set -e
B=/var/lib/nokeyout/firstboot-bundle
LANG="$(cat /var/lib/nokeyout/lang 2>/dev/null | head -1 || true)"
[ "$LANG" = "zh" ] || LANG="en"
mkdir -p /var/lib/nokeyout
[ -e /var/lib/nokeyout/switch-state ] || echo unlocked > /var/lib/nokeyout/switch-state

if [ ! -f /var/lib/nokeyout/.dt-overlay-done ] && ! ls /dev/spidev*.1 >/dev/null 2>&1; then
  mkdir -p /var/lib/nokeyout
  if command -v dtc >/dev/null 2>&1 && [ -d /boot/dtb ]; then
    cat > /tmp/nokeyout-screen.dts <<'DTS'
/dts-v1/;
/plugin/;

/ {
	compatible = "friendlyelec,nanopi-neo3-plus", "rockchip,rk3528";

	fragment@0 {
		target = <&spi1>;
		__overlay__ {
			status = "okay";
			num-cs = <2>;
			pinctrl-names = "default";
			pinctrl-0 = <&spi1_csn0 &spi1_csn1 &spi1_pins>;

			spidev@1 {
				compatible = "armbian,spi-dev";
				reg = <1>;
				spi-max-frequency = <10000000>;
				status = "okay";
			};
		};
	};
};
DTS
    mkdir -p /boot/overlay-user
    dtc -@ -q -I dts -O dtb -o /boot/overlay-user/nokeyout-screen.dtbo /tmp/nokeyout-screen.dts
    if grep -q '^user_overlays=' /boot/armbianEnv.txt 2>/dev/null; then
      grep -q 'nokeyout-screen' /boot/armbianEnv.txt || sed -i 's/^user_overlays=.*/& nokeyout-screen/' /boot/armbianEnv.txt
    else
      echo 'user_overlays=nokeyout-screen' >> /boot/armbianEnv.txt
    fi
    touch /var/lib/nokeyout/.dt-overlay-done
    sync
    systemctl reboot
    exit 0
  fi
fi

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="$(ip route get 1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')"
if [ -x "$B/panel" ]; then
  export NOKEYOUT_SCREEN_MSG="$B/panel"
  "$B/panel" screen-msg start 0 "$LANG" || true
fi
[ -x "$B/install-nokeyout.sh" ] || {
  [ -x "$B/panel" ] && "$B/panel" screen-msg error -1 "$LANG" || true
  exit 1
}
install -d /etc/nokeyout
[ -f /etc/nokeyout/panel.env ] || { touch /etc/nokeyout/panel.env; chown root:nokeyout /etc/nokeyout/panel.env; chmod 640 /etc/nokeyout/panel.env; }
NOKEYOUT_SCREEN=1 bash "$B/install-nokeyout.sh" --offline "$B" --ip "$IP"

# --- NoKeyOut post-install fixes ---------------------------------
if [ -x /var/lib/nokeyout/app/panel-go ] && [ -f /etc/nokeyout/panel.env ]; then
  SPI_DEV=$(ls /dev/spidev*.1 2>/dev/null | head -1 || true)
  [ -n "$SPI_DEV" ] || SPI_DEV=/dev/spidev0.1
  rm -f /var/lib/nokeyout/app/web/firstboot.html
  touch /var/lib/nokeyout/firstboot.done
  sed -i 's/^# SCREEN_BACKEND=.*/SCREEN_BACKEND=st7789/' /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i "s#^# SCREEN_SPI=.*#SCREEN_SPI=$SPI_DEV#" /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i 's/^# SCREEN_DC=.*/SCREEN_DC=128/' /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i 's/^# SCREEN_RST=.*/SCREEN_RST=140/' /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i 's/^# SCREEN_MADCTL=.*/SCREEN_MADCTL=0x00/' /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i 's/^# SCREEN_INVERT=.*/SCREEN_INVERT=1/' /etc/nokeyout/panel.env 2>/dev/null || true
  grep -q '^SCREEN_BACKEND=' /etc/nokeyout/panel.env || echo 'SCREEN_BACKEND=st7789' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_SPI=' /etc/nokeyout/panel.env || echo "SCREEN_SPI=$SPI_DEV" >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_DC=' /etc/nokeyout/panel.env || echo 'SCREEN_DC=128' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_RST=' /etc/nokeyout/panel.env || echo 'SCREEN_RST=140' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_MADCTL=' /etc/nokeyout/panel.env || echo 'SCREEN_MADCTL=0x00' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_INVERT=' /etc/nokeyout/panel.env || echo 'SCREEN_INVERT=1' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_HZ=' /etc/nokeyout/panel.env || echo 'SCREEN_HZ=40000000' >> /etc/nokeyout/panel.env
  cat > /etc/udev/rules.d/50-nokeyout-screen.rules <<'UDEV'
KERNEL=="spidev*.1", GROUP="nokeyout", MODE="0660"
UDEV
  udevadm control --reload-rules 2>/dev/null || true
  chown nokeyout:nokeyout /dev/spidev*.1 2>/dev/null || true
  chmod 660 /dev/spidev*.1 2>/dev/null || true
  systemctl restart nokeyout-screen-gpio.service 2>/dev/null || true
  sudo -u nokeyout /var/lib/nokeyout/app/panel-go screen-gate pass 2>/dev/null || true
  systemctl restart nokeyout-panel 2>/dev/null || true
fi

mkdir -p /var/lib/nokeyout
touch /var/lib/nokeyout/.firstboot-done
systemctl disable nokeyout-firstboot.service 2>/dev/null || true
EOS
  chmod 0755 "$FB"
else
  say "2/3 跳过首次启动部署"
fi

say "3/3 写卡"
ARGS=(--board "$BOARD" --image-url "$IMAGE_URL" --lang "$LANG_CODE")
[ -n "$SHA" ] && ARGS+=(--sha256 "$SHA")
[ -n "$FB" ] && ARGS+=(--firstboot "$FB")
[ -n "$BUNDLE" ] && ARGS+=(--bundle "$BUNDLE")
FLASH="$HERE/flash-tf.sh"
if [ ! -f "$FLASH" ]; then
  FLASH="$TMP/flash-tf.sh"
  curl -fsSL "$TOOLS_BASE/flash-tf.sh" -o "$FLASH" || die "cannot fetch flash-tf.sh"
fi
bash "$FLASH" "${ARGS[@]}"
echo
echo "完成。插卡启动后，firstboot 会用卡内离线包安装；更新在管理面手动执行。"
