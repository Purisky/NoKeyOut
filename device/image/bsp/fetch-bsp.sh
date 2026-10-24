#!/usr/bin/env bash
# fetch-bsp.sh —— 提取 RK3588S 官方 BSP（引导链 + vendor 内核 + 模块）
#
# （U-Boot TPL/SPL + DDR init blob + idbloader + trust）是厂商闭源 blob，
# Rockchip 只以预编译形式发布；armbian 官方镜像把这些 blob 与 vendor 6.1 内核
# 一起打包，是唯一可信且可校验的来源。**不是**把 armbian 当发行版——只用它的
# 引导链与内核，rootfs 由我们的 Buildroot 产物替换。
#
# 产出（bsp/out/，已 .gitignore，脚本可复现）：
#   bootloader-area.img   前 16MB（GPT + idbloader/uboot/trust，dd 原样保留）
#   boot/                 vendor 内核 Image、dtb 目录（含 rk3588-orangepi-5-max.dtb）、模块
#
# 用法：./fetch-bsp.sh [--force]
set -euo pipefail
cd "$(dirname "$0")"
OUT=bsp-out
URL="${BSP_URL:-https://mirrors.tuna.tsinghua.edu.cn/armbian-releases/orangepi5/archive/Armbian_26.8.1_Orangepi5_trixie_vendor_6.1.115_minimal.img.xz}"
SHA="${BSP_SHA256:-8e2c7217a60888c0cf487a85d445b3ee8ea4f533c2293df12d36ccfc6437e040}"
FORCE=0; [ "${1:-}" = "--force" ] && FORCE=1
KVER="6.1.115-vendor-rk35xx"

IMG=/tmp/opi5-bsp.img.xz
if [ ! -s "$IMG" ] || [ "$FORCE" = 1 ]; then
  echo "== 下载官方镜像（322MB，tuna 镜像）:"
  curl -fL --retry 2 -m 2400 "$URL" -o "$IMG"
fi
echo "== 校验 sha256 =="
GOT=$(sha256sum "$IMG" | cut -d' ' -f1)
[ "$GOT" = "$SHA" ] || { echo "sha256 不符: $GOT != $SHA"; exit 1; }
echo "  ✅ $GOT"

echo "== 解包 =="
xz -d -f "$IMG"

RAW=/tmp/opi5-bsp.img
echo "== 提取引导区（前 16MB：GPT + idbloader/uboot/trust）=="
mkdir -p "$OUT"
dd if="$RAW" of="$OUT/bootloader-area.img" bs=1M count=16 status=none

echo "== 提取 vendor 内核 + dtb + 模块（只读挂载）=="
MNT=$(mktemp -d)
trap 'sudo umount "$MNT" 2>/dev/null || true; rm -rf "$MNT"' EXIT
sudo mount -o ro,loop,offset=$((32768*512)) "$RAW" "$MNT"
mkdir -p "$OUT/boot" "$OUT/modules"
sudo cp -a "$MNT/boot/vmlinuz-$KVER"          "$OUT/boot/Image"
sudo cp -a "$MNT/boot/dtb-$KVER"              "$OUT/boot/"
sudo cp -a "$MNT/lib/modules/$KVER"           "$OUT/modules/"
sudo umount "$MNT"; trap - EXIT; rm -rf "$MNT"

echo "== 校验关键件 =="
[ -s "$OUT/boot/Image" ] || { echo "缺内核"; exit 1; }
DTB="$OUT/boot/dtb-$KVER/rockchip/rk3588-orangepi-5-max.dtb"
[ -s "$DTB" ] || { echo "缺 rk3588-orangepi-5-max.dtb（Orange Pi 5 Max 的 DTB）"; exit 1; }
[ -s "$OUT/modules/$KVER/modules.dep" ] || { echo "缺模块"; exit 1; }
ls -la "$OUT" "$OUT/boot" | head -12
echo
echo "✅ BSP 就绪：$OUT/"
echo "   bootloader-area.img  $(stat -c %s "$OUT/bootloader-area.img") 字节（dd 原样，勿改动）"
echo "   Image                $(stat -c %s "$OUT/boot/Image") 字节"
echo "   dtb                  rk3588-orangepi-5-max.dtb（本仓库目标板型）"
echo "   modules              $(du -sh "$OUT/modules/$KVER" | cut -f1)（可裁剪，见 assemble-image.sh）"
