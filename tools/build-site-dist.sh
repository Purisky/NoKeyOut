#!/usr/bin/env bash
# build-site-dist.sh —— 生成 Cloudflare Pages 可直传的最小站点目录
#
#   bash tools/build-site-dist.sh [输出目录]
#   默认输出：dist/site
#
# 只保留教程运行时会请求的文件；测试、文档、KiCad/STEP 源文件、
# 3D review 文件和不参与运行时的备用网格不进入发布包。
# 站点全部来自 web/：主页在根，3D 组装小游戏在 /diy/；
# 另附在线安装/烧录脚本（install-nokeyout.sh、install-tf.sh、flash-tf.sh、
# install-tf.ps1、flash-tf-wsl.sh、boards.json → /tools/）。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/dist/site}"
case "$OUT" in
  "$ROOT"/dist/*) ;;
  *) echo "输出目录必须在 $ROOT/dist/ 下: $OUT" >&2; exit 1 ;;
esac
[ -d "$ROOT/web" ] || { echo "missing web/" >&2; exit 1; }
[ -f "$ROOT/web/index.html" ] || { echo "missing web/index.html" >&2; exit 1; }
[ -f "$ROOT/web/404.html" ] || { echo "missing web/404.html" >&2; exit 1; }

rm -rf "$OUT"
mkdir -p "$OUT"
cp -R "$ROOT/web/." "$OUT/"

mkdir -p "$OUT/tools"
for f in install-nokeyout.sh install-tf.sh flash-tf.sh flash-tf-wsl.sh install-tf.ps1 boards.json nokeyout-screen.dtbo; do
  cp "$ROOT/tools/$f" "$OUT/tools/$f"
done
[ -f "$ROOT/device/panel/web/firstboot.html" ] && cp "$ROOT/device/panel/web/firstboot.html" "$OUT/tools/firstboot.html"

REL_DIR="${NOKEYOUT_RELEASES_DIR:-$ROOT/releases}"
if [ -d "$REL_DIR/stable" ]; then
  mkdir -p "$OUT/releases/stable"
  cp "$REL_DIR/stable/"*.json "$OUT/releases/stable/" 2>/dev/null || true
  cp "$REL_DIR/stable/"*.sig  "$OUT/releases/stable/" 2>/dev/null || true
fi
[ -f "$ROOT/device/update/release.pub" ] && cp "$ROOT/device/update/release.pub" "$OUT/release.pub"

# 开发/源文件，不进 Pages
rm -rf "$OUT/diy/test" "$OUT/diy/levels/level-2-gating.js" "$OUT/diy/levels/level-3-spi.js" "$OUT/diy/levels/level-4-screen.js"
rm -f  "$OUT/diy"/*.md
rm -f  "$OUT/diy/assets/board/models/fbx-data.js"

# Pages 缓存/安全头（位于部署根目录）
cat > "$OUT/_headers" <<'EOS'
/*
  X-Content-Type-Options: nosniff
  Referrer-Policy: strict-origin-when-cross-origin
  Cache-Control: public, max-age=300

/diy/assets/*
  Cache-Control: public, max-age=86400

/assets/*
  Cache-Control: public, max-age=86400

/diy/vendor/*
  Cache-Control: public, max-age=86400

/*.html
  Cache-Control: public, max-age=0, must-revalidate
EOS

COUNT=$(find "$OUT" -type f | wc -l | tr -d ' ')
TOTAL_KB=$(du -sk "$OUT" | awk '{print $1}')
TOTAL_MIB=$(awk -v k="$TOTAL_KB" 'BEGIN{printf "%.2f", k/1024}')
TOO_BIG=$(find "$OUT" -type f -size +25M -print)
if [ -n "$TOO_BIG" ]; then
  echo "❌ 超过 Cloudflare Pages 单文件 25 MiB 限制：" >&2
  printf '%s\n' "$TOO_BIG" >&2
  exit 1
fi
[ "$COUNT" -le 20000 ] || { echo "❌ 文件数 $COUNT 超过 20,000" >&2; exit 1; }

echo "Cloudflare Pages 输出: $OUT"
echo "文件数: $COUNT (限制 20,000)"
echo "总大小: ${TOTAL_MIB} MiB (单文件上限 25 MiB)"
echo "最大文件:"
find "$OUT" -type f -printf '%s %p\n' | sort -nr | head -5 | awk '{printf "  %6.2f MiB  %s\n", $1/1048576, $2}'
