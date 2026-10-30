#!/usr/bin/env bash
# release-manifest.sh —— 为 nokeyout-update 生成并签名 manifest.json
#
#   ./device/release-manifest.sh <tarball> <version> <base-url> <private-key> [channel]
#
# 例：
#   ./device/release-manifest.sh nokeyout-0.3.1-linux-arm64.tar.gz 0.3.1 \
#     https://nokeyout.com/releases/0.3.1 ~/.keys/nokeyout-release.pem stable
#
# 产物写在与 tarball 同目录：manifest.json、manifest.json.sig、SHA256SUMS。
# 私钥绝不进仓库/设备；设备只放公钥（openssl pkey -pubout）。
set -euo pipefail

TAR="${1:?用法: $0 <tarball> <version> <base-url> <private-key> [channel]}"
VER="${2:?version}"
BASE="${3:?base-url}"
KEY="${4:?private-key}"
CHANNEL="${5:-stable}"
[ -f "$TAR" ] || { echo "找不到 $TAR" >&2; exit 1; }
[ -f "$KEY" ] || { echo "找不到私钥 $KEY" >&2; exit 1; }
[[ "$VER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version 必须是 x.y.z" >&2; exit 1; }
if [[ ! "$BASE" =~ ^https:// ]] && [ "${NOKEYOUT_ALLOW_HTTP:-}" != "1" ]; then
  echo "base-url 必须是 https://（测试可设 NOKEYOUT_ALLOW_HTTP=1）" >&2
  exit 1
fi
BASE="${BASE%/}"
NAME="$(basename "$TAR")"
ARCH=""
case "$NAME" in
  *-linux-arm64.tar.gz) ARCH=arm64 ;;
  *-linux-amd64.tar.gz) ARCH=amd64 ;;
  *) echo "文件名需含 -linux-arm64 或 -linux-amd64: $NAME" >&2; exit 1 ;;
esac
SHA=$(sha256sum "$TAR" | awk '{print $1}')
SIZE=$(stat -c%s "$TAR")
OUT_DIR="$(dirname "$TAR")"
OUT="$OUT_DIR/manifest.json"
MIN="${NOKEYOUT_MIN_VERSION:-}"
MAGPIE="${NOKEYOUT_MAGPIE_VERSION:-0.1.2144}"
NOTES="${NOKEYOUT_RELEASE_NOTES:-}"
ISSUED=$(date -u +%Y-%m-%dT%H:%M:%SZ)

json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

cat > "$OUT" <<EOF
{
  "schema": 1,
  "version": "$VER",
  "channel": "$(json_escape "$CHANNEL")",
  "minVersion": "$(json_escape "$MIN")",
  "magpie": "$(json_escape "$MAGPIE")",
  "issuedAt": "$ISSUED",
  "notes": "$(json_escape "$NOTES")",
  "assets": {
    "linux-$ARCH": {
      "url": "$BASE/$NAME",
      "sha256": "$SHA",
      "size": $SIZE
    }
  }
}
EOF

# Ed25519 detached signature (raw 64 bytes)
openssl pkeyutl -sign -inkey "$KEY" -rawin -in "$OUT" -out "$OUT.sig"
chmod 0644 "$OUT" "$OUT.sig"
(cd "$OUT_DIR" && sha256sum "$NAME" > SHA256SUMS)

echo "manifest: $OUT"
echo "signature: $OUT.sig"
echo "sha256: $SHA"
echo "asset: $BASE/$NAME"
