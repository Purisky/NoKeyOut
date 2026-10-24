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
if [ "$DEPLOY" = 1 ]; then
  say "2/3 准备首次启动安装"
  FB="$TMP/nokeyout-firstboot.sh"
  cat > "$FB" <<'EOS'
#!/bin/sh
set -e
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$IP" ] || IP="$(ip route get 1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')"
curl -fL --retry 3 --retry-delay 3 https://nokeyout.com/tools/install-nokeyout.sh -o /tmp/install-nokeyout.sh
NOKEYOUT_SCREEN=1 sh /tmp/install-nokeyout.sh --ip "$IP"
mkdir -p /var/lib/nokeyout
touch /var/lib/nokeyout/.firstboot-done
systemctl disable nokeyout-firstboot.service 2>/dev/null || true
EOS
  chmod 0755 "$FB"
else
  say "2/3 跳过首次启动部署"
fi

say "3/3 写卡"
ARGS=(--board "$BOARD" --image-url "$IMAGE_URL")
[ -n "$SHA" ] && ARGS+=(--sha256 "$SHA")
[ -n "$FB" ] && ARGS+=(--firstboot "$FB")
FLASH="$HERE/flash-tf.sh"
if [ ! -f "$FLASH" ]; then
  FLASH="$TMP/flash-tf.sh"
  curl -fsSL "$TOOLS_BASE/flash-tf.sh" -o "$FLASH" || die "cannot fetch flash-tf.sh"
fi
bash "$FLASH" "${ARGS[@]}"
echo
echo "完成。插卡启动后，firstboot 会自动运行 install-nokeyout.sh。"
