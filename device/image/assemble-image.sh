#!/usr/bin/env bash
# assemble-image.sh —— BSP 引导链 + vendor 内核 + NoKeyOut rootfs → 可烧录镜像
#
#   ./assemble-image.sh <rootfs.tar> <release.tar.gz> <bsp-out>
#
# 布局（GPT）：
#   0 .. 16MB        bootloader 区（BSP 原样：idbloader/uboot/trust，勿动）
#   分区1  boot      vfat，Image + dtb-* + extlinux.conf（U-Boot distro boot 先扫它，
#                   root=UUID 固定；与 armbian 官方镜像同布局）
#   分区2  rootfs    ext4（UUID 固定 270fe48d-…）：Buildroot rootfs + NoKeyOut 二进制
#                   + vendor 内核模块（/lib/modules/<KVER>）+ /boot 副本（后备）
#
# 自建其它板型时用环境变量覆盖（脚本本身不包含多板配置）：
#   KVER=6.1.115-vendor-rk35xx
#   DTB_REL=rockchip/<board>.dtb
#   ROOTFS_MB=768  BOOT_MB not overridable in this version
#   OUT=nokeyout.img
# BSP 目录必须提供：bootloader-area.img、boot/Image、boot/dtb-<KVER>/<DTB_REL>、
# modules/<KVER>。
set -euo pipefail
[ $# -eq 3 ] || { echo "用法: $0 <rootfs.tar> <release.tar.gz> <bsp-out>"; exit 2; }
ROOTFS_TAR="$1"; RELEASE="$2"; BSP="$3"
OUT="${OUT:-nokeyout.img}"
BOOT_MB=256
ROOTFS_MB="${ROOTFS_MB:-768}"
STAGE=$(mktemp -d)
trap 'sudo umount "$STAGE/mnt" 2>/dev/null; sudo umount "$STAGE/chk2" 2>/dev/null; sudo umount "$STAGE/chk" 2>/dev/null; [ -s "$STAGE/loop" ] && sudo losetup -d "$(cat "$STAGE/loop")" 2>/dev/null; rm -rf "$STAGE"' EXIT

echo "== 1/6 解 release 与 rootfs =="
mkdir -p "$STAGE/rel" "$STAGE/root"
tar xzf "$RELEASE" -C "$STAGE/rel"
REL="$STAGE/rel/$(ls "$STAGE/rel" | head -1)"
mkdir -p "$STAGE/root" && tar xf "$ROOTFS_TAR" -C "$STAGE/root"

echo "== 2/6 落 NoKeyOut 二进制（release 包是唯一真相源）=="
install -d "$STAGE/root/usr/local/lib/nokeyout/bin" "$STAGE/root/usr/local/bin" "$STAGE/root/var/lib/nokeyout/app"
install -m 0755 "$REL/panel-go/panel"          "$STAGE/root/var/lib/nokeyout/app/panel-go"
install -m 0755 "$REL/tools/magpie-pull/magpie-pull" "$STAGE/root/usr/local/lib/nokeyout/bin/"
install -m 0755 "$REL/gateway/bin/nokeyout-auth"     "$STAGE/root/usr/local/bin/"
install -m 0755 "$REL/gateway/nokeyout-magpie-cli"   "$STAGE/root/usr/local/bin/"
install -m 0755 "$REL/gateway/nokeyout-upgrade"      "$STAGE/root/usr/local/bin/"
install -m 0755 "$REL/panel/nokeyout-cert.sh"   "$STAGE/root/usr/local/bin/nokeyout-cert"
install -m 0755 "$REL/gateway/update-magpie-binary.sh" "$STAGE/root/usr/local/lib/nokeyout/"
install -m 0755 "$REL/panel/setup.sh"          "$STAGE/root/usr/local/lib/nokeyout/setup.sh"
install -m 0644 "$REL/panel/web/"*.html        "$STAGE/root/var/lib/nokeyout/app/web/" --target-directory="$STAGE/root/var/lib/nokeyout/app/web/" 2>/dev/null || {
  mkdir -p "$STAGE/root/var/lib/nokeyout/app/web"; install -m 0644 "$REL/panel/web/"*.html "$STAGE/root/var/lib/nokeyout/app/web/"; }
# magpie 本体：MAGPIE_SRC= 指定 > 本机正跑的 /usr/local/bin/magpie（解软链）>
# release 自带 magpie-pull 现拉 pinned 版。都没有则镜像仍可用：首启后 setup 会装。
install -d "$STAGE/root/usr/local/lib/nokeyout/bin"
MAGPIE_TARGET="$STAGE/root/usr/local/bin/magpie"
if [ -n "${MAGPIE_SRC:-}" ] && [ -s "$MAGPIE_SRC" ]; then
  install -m 0755 "$MAGPIE_SRC" "$MAGPIE_TARGET"
else
  # 本机正跑的 magpie（readlink -f 解软链：设备上是指向 magpie-v<版本> 的符号链接，
  # file 对软链本身输出 "symbolic link" 不含 ELF——必须先解链）
  MAGPIE_BIN=$(readlink -f /usr/local/bin/magpie 2>/dev/null || echo "")
  if [ -n "$MAGPIE_BIN" ] && [ -s "$MAGPIE_BIN" ] && file "$MAGPIE_BIN" | grep -q ELF; then
    install -m 0755 "$MAGPIE_BIN" "$MAGPIE_TARGET"
  elif [ -x "$REL/tools/magpie-pull/magpie-pull" ]; then
    # 兜底：release 自带的静态直取器现拉 pinned 版（走本机代理）
    "$REL/tools/magpie-pull/magpie-pull" ghcr.io/yetone/magpie:0.1.2144 -o "$MAGPIE_TARGET" || true
  fi
fi
[ -s "$MAGPIE_TARGET" ] && echo "  magpie 已随镜像" || \
  echo "  ⚠️ 未随镜像带 magpie（首启后 setup 装或 MAGPIE_SRC= 重 assemble）"

echo "== 3/6 boot 分区内容 =="
mkdir -p "$STAGE/boot"
cp "$BSP/boot/Image"            "$STAGE/boot/Image"
cp -a "$BSP/boot/dtb-"*/        "$STAGE/boot/"
mkdir -p "$STAGE/boot/extlinux"
cat > "$STAGE/boot/extlinux/extlinux.conf" <<EOF
label nokeyout
  kernel /Image
  fdt /$(ls "$BSP/boot" | grep -oE "dtb-[0-9.]+-vendor-rk35xx" | head -1)/$DTB_REL
  append root=UUID=270fe48d-0f82-4921-87e8-aaca0e442eba rootwait rw console=${CONSOLE:-ttyS2,1500000} console=tty1 cma=256M
EOF
echo "  用 DTB: $DTB_REL（默认 Orange Pi 5 Max；换板型用 DTB_REL=... 覆盖）"

echo "== 4/6 rootfs ext4（UUID 固定）=="
# vendor 内核模块：RK3588 的网口/USB 等驱动在 vendor 内核里普遍 =m；
# 不随镜像带上 = extlinux 里 root=UUID 能挂上根文件系统也没网（面板起不来）。
KVER="${KVER:-6.1.115-vendor-rk35xx}"
DTB_REL="${DTB_REL:-rockchip/rk3588-orangepi-5-max.dtb}"
CONSOLE="${CONSOLE:-ttyS2,1500000}"
if [ -d "$BSP/modules/$KVER" ]; then
  install -d "$STAGE/root/lib/modules"
  cp -a "$BSP/modules/$KVER" "$STAGE/root/lib/modules/"
else
  echo "  ⚠️ 缺 $BSP/modules/$KVER——镜像将无 vendor 驱动模块"
fi
dd if=/dev/zero of="$STAGE/rootfs.ext4" bs=1M count="$ROOTFS_MB" status=none
mkfs.ext4 -q -F -U 270fe48d-0f82-4921-87e8-aaca0e442eba -b 4096 "$STAGE/rootfs.ext4"
mkdir -p "$STAGE/mnt"
sudo mount -o loop "$STAGE/rootfs.ext4" "$STAGE/mnt"
sudo cp -a "$STAGE/root/." "$STAGE/mnt/"
sudo mkdir -p "$STAGE/mnt/boot"
sudo cp -a "$STAGE/boot/." "$STAGE/mnt/boot/"
sudo mkdir -p "$STAGE/mnt/boot/extlinux"
sudo cp "$STAGE/boot/extlinux/extlinux.conf" "$STAGE/mnt/boot/extlinux/"
sudo umount "$STAGE/mnt"

echo "== 5/6 组装镜像（引导区原样 + boot + rootfs）=="
BOOT_START=$((16*1024*1024/512))
BOOT_END=$((BOOT_START + BOOT_MB*1024*1024/512))
ROOT_START=$((BOOT_END + 2048))
ROOT_END=$((ROOT_START + ROOTFS_MB*1024*1024/512 - 1))
# 镜像总扇区 = rootfs 末扇区 + GPT 备份头/条目（33 扇区）+1 对齐余量：
# 少了这段 sgdisk 会报「最后一个可用的 GPT 扇区不足」
IMG_SECTORS=$((ROOT_END + 34))
dd if="$BSP/bootloader-area.img" of="$OUT" bs=1M status=none
truncate -s $((IMG_SECTORS*512)) "$OUT"
# GPT 分区表（BSP 区里那份 Armbian GPT 描述的是大盘，sgdisk 会自动重建）
sgdisk -a 1 -n 1:$BOOT_START:$BOOT_END -t 1:0700 -c 1:"boot" \
       -n 2:$ROOT_START:$ROOT_END -t 2:8300 -c 2:"rootfs" "$OUT" 2>/dev/null || \
  { sfdisk "$OUT" <<EOF
label: gpt
start=$BOOT_START, size=$((BOOT_MB*1024*1024/512)), type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name=boot
start=$ROOT_START, size=$((ROOTFS_MB*1024*1024/512)), type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=rootfs
EOF
  }
# 分区1（boot vfat）：U-Boot distro boot 先扫 boot 分区（与 armbian 官方同布局）；
# rootfs /boot 下也各有一份（作后备）。两边内容同源。
dd if=/dev/zero of="$STAGE/boot.vfat" bs=1M count="$BOOT_MB" status=none
mkfs.vfat -n NOKEYOUT "$STAGE/boot.vfat" >/dev/null
sudo mount -o loop "$STAGE/boot.vfat" "$STAGE/mnt"
# vfat 没有所有权/权限位，cp -a 的 chown 会整批报错（数据其实已复制）——用 -r
sudo cp -r "$STAGE/boot/." "$STAGE/mnt/"
sudo umount "$STAGE/mnt"
dd if="$STAGE/boot.vfat" of="$OUT" bs=512 seek="$BOOT_START" conv=notrunc status=none
dd if="$STAGE/rootfs.ext4" of="$OUT" bs=512 seek="$ROOT_START" conv=notrunc status=none

echo "== 6/6 校验 =="
sudo losetup -P -f --show "$OUT" > "$STAGE/loop" || true
LOOP=$(cat "$STAGE/loop" 2>/dev/null || echo "")
[ -n "$LOOP" ] || { echo "  ⚠️ losetup 不可用，跳过挂载校验"; exit 0; }
sudo mkdir -p "$STAGE/chk" "$STAGE/chk2"
sudo mount "${LOOP}p2" "$STAGE/chk2"
BOOT_MOUNTED=0
if sudo mount "${LOOP}p1" "$STAGE/chk"; then BOOT_MOUNTED=1; fi
RC=0
chk() { if [ -e "$1" ]; then echo "  ✅ $2"; else echo "  ❌ $2"; RC=1; fi; }
if [ "$BOOT_MOUNTED" = 1 ]; then
  chk "$STAGE/chk/Image" "boot: /Image"
  chk "$STAGE/chk/dtb-"*/$DTB_REL "boot: dtb-*/$DTB_REL"
  chk "$STAGE/chk/extlinux/extlinux.conf" "boot: /extlinux/extlinux.conf"
else
  echo "  ⚠️ boot 分区未挂载（vfat 驱动缺失？），跳过 boot 侧校验"
fi
chk "$STAGE/chk2/var/lib/nokeyout/app/panel-go" "rootfs: panel-go"
chk "$STAGE/chk2/usr/local/lib/nokeyout/bin/magpie-pull" "rootfs: magpie-pull"
chk "$STAGE/chk2/usr/local/bin/nokeyout-auth" "rootfs: nokeyout-auth(C)"
chk "$STAGE/chk2/usr/local/bin/magpie" "rootfs: magpie"
chk "$STAGE/chk2/boot/Image" "rootfs: /boot/Image"
chk "$STAGE/chk2/boot/extlinux/extlinux.conf" "rootfs: /boot/extlinux/extlinux.conf"
chk "$STAGE/chk2/lib/modules/$KVER/modules.dep" "rootfs: lib/modules/$KVER"
chk "$STAGE/chk2/etc/init.d/rcS" "rootfs: /etc/init.d/rcS"
chk "$STAGE/chk2/etc/inittab" "rootfs: /etc/inittab"
UUID_GOT=$(sudo dumpe2fs -h "${LOOP}p2" 2>/dev/null | sed -n 's/^Filesystem UUID: *//p' | head -1)
if [ "$UUID_GOT" = "270fe48d-0f82-4921-87e8-aaca0e442eba" ]; then
  echo "  ✅ ext4 UUID: $UUID_GOT"
else
  echo "  ❌ ext4 UUID: ${UUID_GOT:-<none>}"; RC=1
fi
sudo umount "$STAGE/chk2"
if [ "$BOOT_MOUNTED" = 1 ]; then sudo umount "$STAGE/chk"; fi
sudo losetup -d "$LOOP"
[ "$RC" = 0 ] || { echo "❌ 校验未全过"; exit 1; }
echo
echo "✅ 镜像就绪：$OUT（$(du -h "$OUT" | cut -f1)）"
echo "   烧录：sudo dd if=$OUT of=/dev/sdX bs=8M status=progress conv=fsync"
echo "   首次启动后跑 /usr/local/lib/nokeyout/setup.sh（证书/用户/首启，幂等）"
