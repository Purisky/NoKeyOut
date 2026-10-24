#!/usr/bin/env bash
# build.sh —— 构建 NoKeyOut 最小 rootfs（Buildroot）
#
#   ./build.sh            # 首次：下载 buildroot → 应用 defconfig/overlay → 构建
#
# 产出：output/images/rootfs.tar（由 assemble-image.sh 装进 ext4）。
# 环境：需要在目标 arch（arm64）上构建，或已装交叉工具链的机器。
set -euo pipefail
# HERE 必须在任何 cd 之前算（相对 BASH_SOURCE + 已 cd 的 cwd 会算错目录）
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"
BR_VER="${BR_VER:-2026.05.3}"
BR_DIR="${BR_DIR:-/tmp/buildroot-build-$BR_VER}"

if [ ! -d "$BR_DIR/.git" ] && [ ! -f "$BR_DIR/Makefile" ]; then
  echo "== 下载 buildroot $BR_VER =="
  mkdir -p "$(dirname "$BR_DIR")"
  T=/tmp/buildroot-$BR_VER.tar.gz
  [ -s "$T" ] || curl -fL --retry 2 -m 900 \
    "https://github.com/buildroot/buildroot/archive/refs/tags/$BR_VER.tar.gz" -o "$T"
  rm -rf "$BR_DIR"
  tar xzf "$T" -C "$(dirname "$BR_DIR")"
  mv "$(dirname "$BR_DIR")/buildroot-$BR_VER" "$BR_DIR"
fi

cd "$BR_DIR"
echo "== 应用 NoKeyOut defconfig + overlay =="
make defconfig BR2_DEFCONFIG="$HERE/defconfig"

# overlay：init 脚本 / 服务监管 / sudoers / panel.env（静态配置，不变）
mkdir -p "$BR_DIR/output/target"
cp -a "$HERE/overlay/." "$BR_DIR/output/target/"
# busybox 片段：$(CONFIG_DIR) 是构建树根，片段要先落到 BR_DIR 才能被 kconfig 找到
[ -f "$HERE/busybox.fragment" ] && cp "$HERE/busybox.fragment" "$BR_DIR/"

echo "== 构建（约 15-30 分钟）:"
make -j"$(nproc)"

echo "== 产物 =="
ls -la "$BR_DIR/output/images/rootfs.tar"
echo
echo "👉 下一步：../assemble-image.sh <rootfs.tar> <release.tar.gz> <bsp 目录>"
