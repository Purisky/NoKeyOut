#!/usr/bin/env bash
#
# 加密对象：magpie 的 /config（providers/真实 Key/sign-ins/plugins）+ panel 的 secrets/。
# 不加密 rootfs：明文根 + 加密数据卷 = 上游升级只动根，密钥材料全在卷里。
# 解锁口令 = 设备账号口令（人脑秘密，不落盘）；断电即锁（LUKS key 驻内存）。
#
# 用法（生产，整分区/盘）：
#   sudo ./setup-luks-volume.sh --dev /dev/sda2 --migrate-from /home/nokeyout/magpie-config
# 用法（冒烟/测试，环回文件，不动任何真盘）：
#   sudo ./setup-luks-volume.sh --file /tmp/luks.img --size 256M --mount /tmp/luks-mnt \
#        --key-file /tmp/luks.key --no-automount --migrate-from /tmp/dummy-config
#
# 参数：
#   --dev <块设备>        目标分区/盘（与 --file 二选一）
#   --file <路径>         环回镜像文件（测试用）
#   --size <尺寸>         环回镜像大小（默认 512M）
#   --name <mapper名>     默认 nokeyout-data
#   --mount <挂载点>      默认 /var/lib/nokeyout/data
#   --key-file <路径>     批模式密钥文件（交互模式不传则口令输入两遍）
#   --migrate-from <目录> 把已有数据拷入卷（magpie /config 等）
#   --no-automount        不写 crypttab/fstab（测试）
#   --force               目标已挂载/已格式化也重来（会毁数据！）
set -euo pipefail

NAME=nokeyout-data; MOUNT=/var/lib/nokeyout/data
DEV=""; FILE=""; SIZE=512M; KEYFILE=""; MIGRATE=""; AUTOMOUNT=1; FORCE=0
while [ $# -gt 0 ]; do case "$1" in
  --dev) DEV="$2"; shift 2;; --file) FILE="$2"; shift 2;; --size) SIZE="$2"; shift 2;;
  --name) NAME="$2"; shift 2;; --mount) MOUNT="$2"; shift 2;;
  --key-file) KEYFILE="$2"; shift 2;; --migrate-from) MIGRATE="$2"; shift 2;;
  --no-automount) AUTOMOUNT=0; shift;; --force) FORCE=1; shift;;
  *) echo "未知参数 $1"; exit 1;; esac; done

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }
command -v cryptsetup >/dev/null || { echo "缺 cryptsetup：apt-get install -y cryptsetup"; exit 1; }
[ -n "$DEV" ] || [ -n "$FILE" ] || { echo "必须给 --dev 或 --file"; exit 1; }

MAPPER="/dev/mapper/$NAME"
if [ -n "$FILE" ]; then
  say "1/6 环回镜像（测试模式）"
  [ -e "$FILE" ] && [ "$FORCE" = 0 ] && { echo "$FILE 已存在（--force 覆盖）"; exit 1; }
  truncate -s "$SIZE" "$FILE"
  DEV=$(losetup --find --show "$FILE")
  echo "  loop: $DEV → $FILE"
else
  say "1/6 目标块设备"
  echo "  ⚠️ 将格式化 $DEV —— 上面的数据会没"
  lsblk "$DEV" 2>/dev/null || true
fi

say "2/6 LUKS2 格式化（argon2id）"
if [ -n "$KEYFILE" ]; then
  cryptsetup luksFormat --batch-mode --type luks2 --key-file "$KEYFILE" "$DEV"
else
  cryptsetup luksFormat --type luks2 "$DEV"   # 交互：口令两遍
fi
BACKUP="${DEV##*/}.luks-header.backup"
cryptsetup luksHeaderBackup "$DEV" --header-backup-file "/var/backups/nokeyout/$BACKUP" 2>/dev/null \
  || cryptsetup luksHeaderBackup "$DEV" --header-backup-file "$BACKUP"
echo "  头备份: $BACKUP（离线保存；丢了头 = 数据没了）"

say "3/6 打开 + 建文件系统"
[ -e "$MAPPER" ] && { cryptsetup luksClose "$NAME" || true; }
if [ -n "$KEYFILE" ]; then cryptsetup luksOpen --key-file "$KEYFILE" "$DEV" "$NAME"
else cryptsetup luksOpen "$DEV" "$NAME"; fi
if [ "$FORCE" = 1 ] || ! blkid "$MAPPER" | grep -q ext4; then
  mkfs.ext4 -F -L nokeyout-data "$MAPPER"
fi
mkdir -p "$MOUNT"; mount "$MAPPER" "$MOUNT"
chmod 700 "$MOUNT"

say "4/6 迁入数据（可选）"
if [ -n "$MIGRATE" ]; then
  [ -d "$MIGRATE" ] || { echo "  --migrate-from $MIGRATE 不存在"; exit 1; }
  cp -a "$MIGRATE"/. "$MOUNT"/
  echo "  已迁入: $(du -sh "$MOUNT" | cut -f1)"
else
  echo "  （无 --migrate-from，空卷）"
fi

say "5/6 开机自动挂载（生产）"
if [ "$AUTOMOUNT" = 1 ]; then
  UUID=$(blkid -s UUID -o value "$DEV")
  grep -q "$NAME" /etc/crypttab 2>/dev/null || echo "$NAME UUID=$UUID none luks" >> /etc/crypttab
  grep -q " $MOUNT " /etc/fstab 2>/dev/null || echo "$MAPPER $MOUNT ext4 defaults,nofail 0 2" >> /etc/fstab
  echo "  crypttab + fstab 已写入（解锁交互在 initramfs）"
else
  echo "  --no-automount：未写 crypttab/fstab"
fi

say "6/6 验证"
df -h "$MOUNT" | tail -1
[ -n "$MIGRATE" ] && find "$MOUNT" -maxdepth 1 | head -5
echo "  关闭: cryptsetup luksClose $NAME  （loop 还要 losetup -d $DEV）"
echo "  重开: cryptsetup luksOpen $DEV $NAME && mount $MAPPER $MOUNT"
