#!/usr/bin/env bash
# flash-tf.sh - download/write a NanoPi NEO3 Plus image to a microSD card,
# then create a login user and enable SSH on the flashed rootfs.
#
# Example (script fetched online; board id, disk, warning and password are prompted):
#   curl -fsSL https://nokeyout.com/tools/flash-tf.sh -o /tmp/flash-tf.sh
#   sudo bash /tmp/flash-tf.sh
#
# There is no default board. The script asks for the board id, lists removable
# disks for manual selection, shows a format warning, then asks for the password.
# The hostname is always `nokeyout`; there is no --hostname option.
set -euo pipefail

DEVICE=""
DEV_SIZE=""
DEV_MODEL=""
IMAGE=""
IMAGE_URL=""
IMAGE_SHA256=""
ROOT_PART=""
USER_NAME="nokeyout"
BOARD=""
USER_HOSTNAME="nokeyout"
CONFIGURE_ONLY=0
FIRSTBOOT=""
BUNDLE=""
LANG_CODE="${NOKEYOUT_LANG:-}"
RELEASE_BASE="${NOKEYOUT_IMAGE_BASE:-https://nokeyout.com/releases/images}"
CACHE_DIR="${NOKEYOUT_CACHE:-${XDG_CACHE_HOME:-/root/.cache}}/nokeyout-images"
MNT=""
BOOT_MNT=""
TMP_IMG=""
WORK=""

die() { echo "ERROR: $*" >&2; exit 1; }
say() { echo "==> $*"; }

usage() {
  cat <<'EOF'
Usage: flash-tf.sh [options]

  --device PATH        Target block device; otherwise list removable disks and ask
  --board NAME         Board id used to resolve the latest image; asked if omitted
  --image PATH         Local image (.img, .img.gz, .img.xz, .zip)
  --image-url URL      Download this image first
  --sha256 HEX         Optional SHA-256 of the downloaded image
  --root-part PATH     Rootfs partition (default: largest ext4 partition)
  --configure-only     Do not write the image; only configure the mounted rootfs
  --firstboot PATH     Install this script as a one-shot first-boot service
  --bundle DIR         Copy an offline firstboot bundle into the rootfs
  --lang zh|en         UI language written to /var/lib/nokeyout/lang (default: host locale)
  -h, --help           Show this help

Defaults: user and hostname are `nokeyout`; there is no default board.
The script lists removable disks, requires an explicit selection, shows a
format warning that must be acknowledged with YES, then asks for the password
twice (not echoed). It refuses the system disk, mounted devices, and non-block devices.
It enables PasswordAuthentication and the SSH service by editing the rootfs;
the image is not chrooted, so ARM binaries are not executed on the host.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --device) DEVICE="${2:-}"; shift 2 ;;
    --board) BOARD="${2:-}"; shift 2 ;;
    --image) IMAGE="${2:-}"; shift 2 ;;
    --image-url) IMAGE_URL="${2:-}"; shift 2 ;;
    --sha256) IMAGE_SHA256="${2:-}"; shift 2 ;;
    --root-part) ROOT_PART="${2:-}"; shift 2 ;;
    --configure-only) CONFIGURE_ONLY=1; shift ;;
    --firstboot) FIRSTBOOT="${2:-}"; shift 2 ;;
    --bundle) BUNDLE="${2:-}"; shift 2 ;;
    --lang) LANG_CODE="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (use --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
for c in lsblk findmnt mount umount dd sync openssl blkid partprobe; do
  command -v "$c" >/dev/null 2>&1 || die "missing tool: $c"
done

ROOT_SRC="$(findmnt -n -o SOURCE / | sed 's/\[.*//')"
ROOT_DISK="$(lsblk -no PKNAME "$ROOT_SRC" 2>/dev/null || true)"

# 没有默认板型：镜像必须按用户输入的 board id 解析
if [ "$CONFIGURE_ONLY" -eq 0 ] && [ -z "$BOARD" ]; then
  printf 'Board id (e.g. neo3plus / opi5max): '; read -r BOARD
fi
if [ "$CONFIGURE_ONLY" -eq 0 ]; then
  [ -n "$BOARD" ] || die "board id is required"
  [[ "$BOARD" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || die "invalid board: $BOARD"
fi

# 识别可移动盘；不自动选择，必须由用户确认
CANDIDATES=()
while read -r dev_name dev_rm dev_size dev_model; do
  [ "$dev_rm" = "1" ] || continue
  [ -n "$ROOT_DISK" ] && [ "$dev_name" = "$ROOT_DISK" ] && continue
  CANDIDATES+=("/dev/$dev_name|$dev_size|$dev_model")
done < <(lsblk -dno NAME,RM,SIZE,MODEL)
[ "${#CANDIDATES[@]}" -gt 0 ] || die "no removable disk found; insert the TF card reader first"

if [ -n "$DEVICE" ]; then
  DEV_INFO="$(lsblk -dno NAME,RM,SIZE,MODEL "$DEVICE" 2>/dev/null || true)"
  [ -n "$DEV_INFO" ] || die "not a block device: $DEVICE"
  read -r _ DEV_RM DEV_SIZE DEV_MODEL <<< "$DEV_INFO"
  [ "$DEV_RM" = "1" ] || die "not a removable disk: $DEVICE"
else
  echo "Select the target TF card (all data will be erased):"
  SEL=0
  for entry in "${CANDIDATES[@]}"; do
    IFS='|' read -r cand_dev cand_size cand_model <<< "$entry"
    SEL=$((SEL + 1))
    printf '  [%d] %-12s %-8s %s\n' "$SEL" "$cand_dev" "$cand_size" "$cand_model"
  done
  printf 'Select target disk [1-%d]: ' "${#CANDIDATES[@]}"
  read -r PICK
  [[ "$PICK" =~ ^[0-9]+$ ]] || die "invalid selection"
  [ "$PICK" -ge 1 ] && [ "$PICK" -le "${#CANDIDATES[@]}" ] || die "invalid selection: $PICK"
  IFS='|' read -r DEVICE DEV_SIZE DEV_MODEL <<< "${CANDIDATES[$((PICK - 1))]}"
fi

[ -b "$DEVICE" ] || die "not a block device: $DEVICE"
DEV_BASE="$(basename "$DEVICE")"
if [ -n "$ROOT_DISK" ] && [ "$DEV_BASE" = "$ROOT_DISK" ]; then
  die "refusing the system disk: $DEVICE"
fi
if lsblk -ln -o MOUNTPOINT "$DEVICE" 2>/dev/null | grep -qv '^$'; then
  die "device (or a partition) is mounted; unmount it first: lsblk $DEVICE"
fi

if [ "$CONFIGURE_ONLY" -eq 0 ]; then
  printf '\nWARNING: %s (%s %s) will be ERASED and reformatted.\n' "$DEVICE" "$DEV_SIZE" "$DEV_MODEL"
  printf 'All partitions and data on this disk will be lost. Type YES to continue: '
  read -r CONFIRM
  [ "$CONFIRM" = "YES" ] || die "cancelled"
fi

say "Target:   $DEVICE ($DEV_SIZE $DEV_MODEL)"
lsblk -o NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINT,MODEL,SERIAL "$DEVICE" 2>/dev/null || true
say "Board:    ${BOARD:-—}"
say "User:     $USER_NAME"
say "Hostname: $USER_HOSTNAME"

printf 'Password for %s: ' "$USER_NAME"; read -rsp '' PW1; echo
printf 'Confirm password: '; read -rsp '' PW2; echo
[ "$PW1" = "$PW2" ] || die "passwords do not match"
[ -n "$PW1" ] || die "empty password"
HASH="$(openssl passwd -6 "$PW1")"
unset PW1 PW2

json_field() {
  sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$1" | head -1
}

resolve_latest() {
  mkdir -p "$CACHE_DIR"
  META="$CACHE_DIR/${BOARD}-latest.json"
  say "Resolving latest image for board $BOARD"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$RELEASE_BASE/$BOARD/latest.json" -o "$META.part"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$META.part" "$RELEASE_BASE/$BOARD/latest.json"
  else
    die "need curl or wget"
  fi
  mv "$META.part" "$META"
  VERSION="$(json_field "$META" version)"
  FILE="$(json_field "$META" file)"
  MANIFEST_SHA="$(json_field "$META" sha256)"
  [ -n "$VERSION" ] || die "latest.json missing version: $RELEASE_BASE/$BOARD/latest.json"
  [ -n "$FILE" ] || FILE="nokeyout_${BOARD}_${VERSION}.img"
  case "$FILE" in
    "nokeyout_${BOARD}_${VERSION}.img"|"nokeyout_${BOARD}_${VERSION}".img.*) ;;
    *) die "latest.json file name mismatch (expected nokeyout_${BOARD}_${VERSION}.img[.xz]): $FILE" ;;
  esac
  IMAGE_URL="$RELEASE_BASE/$BOARD/$VERSION/$FILE"
  [ -n "$IMAGE_SHA256" ] || IMAGE_SHA256="$MANIFEST_SHA"
}

if [ "$CONFIGURE_ONLY" -eq 0 ]; then
  if [ -z "$IMAGE" ] && [ -z "$IMAGE_URL" ]; then
    resolve_latest
  fi
  say "Image:    ${IMAGE_URL:-$IMAGE}"
fi

cleanup() {
  if [ -n "$MNT" ] && mountpoint -q "$MNT" 2>/dev/null; then
    umount "$MNT" 2>/dev/null || true
  fi
  [ -n "$MNT" ] && rmdir "$MNT" 2>/dev/null || true
  [ -n "$WORK" ] && rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

if [ "$CONFIGURE_ONLY" -eq 0 ]; then
  if [ -n "$IMAGE_URL" ]; then
    mkdir -p "$CACHE_DIR"
    IMAGE="$CACHE_DIR/$(basename "${IMAGE_URL%%\?*}")"
    cache_ok=0
    if [ -s "$IMAGE" ]; then
      if [ -n "$IMAGE_SHA256" ]; then
        got="$(sha256sum "$IMAGE" | awk '{print $1}')"
        if [ "$got" = "$IMAGE_SHA256" ]; then
          cache_ok=1
        else
          say "Cached image checksum mismatch; downloading again"
          rm -f "$IMAGE"
        fi
      else
        cache_ok=1
      fi
    fi
    if [ "$cache_ok" = 1 ]; then
      say "Using cached $IMAGE"
    else
      say "Downloading $IMAGE_URL (resume if a .part exists)"
      if command -v curl >/dev/null 2>&1; then
        if [ -s "$IMAGE.part" ]; then
          curl -fL --retry 3 --retry-delay 2 -C - -o "$IMAGE.part" "$IMAGE_URL" || \
            curl -fL --retry 3 --retry-delay 2 -o "$IMAGE.part" "$IMAGE_URL" || die "download failed"
        else
          curl -fL --retry 3 --retry-delay 2 -o "$IMAGE.part" "$IMAGE_URL" || die "download failed"
        fi
      elif command -v wget >/dev/null 2>&1; then
        wget -c -O "$IMAGE.part" "$IMAGE_URL" || die "download failed"
      else
        die "need curl or wget"
      fi
      mv "$IMAGE.part" "$IMAGE"
    fi
  fi
  [ -f "$IMAGE" ] || die "image not found: $IMAGE"
  if [ -n "$IMAGE_SHA256" ]; then
    got="$(sha256sum "$IMAGE" | awk '{print $1}')"
    if [ "$got" != "$IMAGE_SHA256" ]; then
      rm -f "$IMAGE" "$IMAGE.part"
      die "sha256 mismatch: got $got"
    fi
  fi

  case "$IMAGE" in
    *.gz)
      command -v gzip >/dev/null 2>&1 || die "missing tool: gzip"
      TMP_IMG="$CACHE_DIR/$(basename "${IMAGE%.gz}").raw"
      if [ -s "$TMP_IMG" ]; then say "Using cached $TMP_IMG"; else
        say "Decompressing gzip"; gzip -dc "$IMAGE" > "$TMP_IMG.part" && mv "$TMP_IMG.part" "$TMP_IMG"
      fi
      ;;
    *.xz)
      command -v xz >/dev/null 2>&1 || die "missing tool: xz"
      TMP_IMG="$CACHE_DIR/$(basename "${IMAGE%.xz}").raw"
      if [ -s "$TMP_IMG" ]; then say "Using cached $TMP_IMG"; else
        say "Decompressing xz"; xz -dc "$IMAGE" > "$TMP_IMG.part" && mv "$TMP_IMG.part" "$TMP_IMG"
      fi
      ;;
    *.zip)
      command -v unzip >/dev/null 2>&1 || die "missing tool: unzip"
      TMP_IMG="$CACHE_DIR/nokeyout-image.raw"
      if [ -s "$TMP_IMG" ]; then say "Using cached $TMP_IMG"; else
        say "Extracting zip"; unzip -p "$IMAGE" '*.img' > "$TMP_IMG.part" && mv "$TMP_IMG.part" "$TMP_IMG" || die "no .img in zip"
      fi
      ;;
    *) TMP_IMG="$IMAGE" ;;
  esac

  say "Writing $TMP_IMG -> $DEVICE"
  dd if="$TMP_IMG" of="$DEVICE" bs=8M conv=fsync status=progress
  sync
  partprobe "$DEVICE" 2>/dev/null || true
  sleep 1
fi

if [ -z "$ROOT_PART" ]; then
  ROOT_PART="$(lsblk -b -rno NAME,FSTYPE,SIZE "$DEVICE" \
    | awk '$2=="ext4" {print $3, "/dev/"$1}' | sort -nr | head -1 | awk '{print $2}')"
fi
[ -n "$ROOT_PART" ] && [ -b "$ROOT_PART" ] || die "could not find rootfs partition; pass --root-part"
say "Rootfs: $ROOT_PART"

WORK="$(mktemp -d)"
MNT="$WORK/root"
mkdir -p "$MNT"
mount -o rw "$ROOT_PART" "$MNT"

BOOT_PART="$(lsblk -rno NAME,LABEL,FSTYPE "$DEVICE" | awk '$2=="armbi_boot" && $3=="ext4"{print "/dev/"$1; exit}')"
if [ -z "$BOOT_PART" ]; then
  BOOT_PART="$(lsblk -b -rno NAME,FSTYPE,SIZE "$DEVICE" | awk '$2=="ext4"{print $3, "/dev/"$1}' | sort -n | head -1 | awk '{print $2}')"
fi
if [ -n "$BOOT_PART" ] && [ -b "$BOOT_PART" ]; then
  mkdir -p "$MNT/boot"
  mount -o rw "$BOOT_PART" "$MNT/boot"
  BOOT_MNT="$MNT/boot"
fi

PASSWD="$MNT/etc/passwd"
SHADOW="$MNT/etc/shadow"
GROUP="$MNT/etc/group"
[ -f "$PASSWD" ] && [ -f "$GROUP" ] || die "not a Linux rootfs (missing /etc/passwd or /etc/group)"

if grep -q "^$USER_NAME:" "$PASSWD"; then
  USER_ID="$(awk -F: -v u="$USER_NAME" '$1==u{print $3}' "$PASSWD")"
  GROUP_ID="$(awk -F: -v u="$USER_NAME" '$1==u{print $4}' "$PASSWD")"
  say "User exists; updating password/groups"
else
  USER_ID=1000
  while awk -F: -v id="$USER_ID" '$3==id{found=1} END{exit !found}' "$PASSWD"; do USER_ID=$((USER_ID+1)); done
  GROUP_ID=1000
  while awk -F: -v id="$GROUP_ID" '$3==id{found=1} END{exit !found}' "$GROUP"; do GROUP_ID=$((GROUP_ID+1)); done
  SHELL_BIN="/bin/bash"; [ -x "$MNT/bin/bash" ] || SHELL_BIN="/bin/sh"
  printf '%s:x:%s:%s:%s:/home/%s:%s\n' "$USER_NAME" "$USER_ID" "$GROUP_ID" "$USER_NAME" "$USER_NAME" "$SHELL_BIN" >> "$PASSWD"
  printf '%s:x:%s:\n' "$USER_NAME" "$GROUP_ID" >> "$GROUP"
  say "Created user $USER_NAME (uid=$USER_ID gid=$GROUP_ID)"
fi

if [ -f "$SHADOW" ]; then
  if grep -q "^$USER_NAME:" "$SHADOW"; then
    awk -F: -v u="$USER_NAME" -v h="$HASH" 'BEGIN{OFS=":"} $1==u{$2=h} {print}' "$SHADOW" > "$SHADOW.tmp"
    mv "$SHADOW.tmp" "$SHADOW"
  else
    printf '%s:%s:19000:0:99999:7:::\n' "$USER_NAME" "$HASH" >> "$SHADOW"
  fi
  chmod 640 "$SHADOW" 2>/dev/null || true
else
  die "missing /etc/shadow"
fi

# Add to sudo (Debian/Ubuntu) or wheel (fallback).
for grp in sudo wheel; do
  if grep -q "^$grp:" "$GROUP"; then
    awk -F: -v OFS=: -v u="$USER_NAME" -v g="$grp" '
      $1==g { if ($4=="") $4=u; else if (index(","$4",", ","u",")==0) $4=$4","u }
      { print }' "$GROUP" > "$GROUP.tmp"
    mv "$GROUP.tmp" "$GROUP"
    break
  fi
done

install -d -m 700 -o "$USER_ID" -g "$GROUP_ID" "$MNT/home/$USER_NAME" "$MNT/home/$USER_NAME/.ssh" 2>/dev/null || {
  mkdir -p "$MNT/home/$USER_NAME/.ssh"
  chmod 700 "$MNT/home/$USER_NAME" "$MNT/home/$USER_NAME/.ssh"
  chown -R "$USER_ID:$GROUP_ID" "$MNT/home/$USER_NAME"
}
: > "$MNT/home/$USER_NAME/.ssh/authorized_keys"
chmod 600 "$MNT/home/$USER_NAME/.ssh/authorized_keys"
chown -R "$USER_ID:$GROUP_ID" "$MNT/home/$USER_NAME"

SSHD="$MNT/etc/ssh/sshd_config"
if [ -f "$SSHD" ]; then
  sed -i -E 's/^[#[:space:]]*PasswordAuthentication .*/PasswordAuthentication yes/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*PubkeyAuthentication .*/PubkeyAuthentication yes/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*PermitRootLogin .*/PermitRootLogin no/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*UsePAM .*/UsePAM yes/' "$SSHD"
  grep -q '^PasswordAuthentication yes' "$SSHD" || echo 'PasswordAuthentication yes' >> "$SSHD"
  grep -q '^PubkeyAuthentication yes' "$SSHD" || echo 'PubkeyAuthentication yes' >> "$SSHD"
  grep -q '^PermitRootLogin no' "$SSHD" || echo 'PermitRootLogin no' >> "$SSHD"
  grep -q '^UsePAM yes' "$SSHD" || echo 'UsePAM yes' >> "$SSHD"
fi

# Enable ssh.service (Debian/Ubuntu) or sshd.service without running ARM systemd.
WANTS="$MNT/etc/systemd/system/multi-user.target.wants"
mkdir -p "$WANTS"
if [ -e "$MNT/lib/systemd/system/ssh.service" ]; then
  ln -sf /lib/systemd/system/ssh.service "$WANTS/ssh.service"
elif [ -e "$MNT/lib/systemd/system/sshd.service" ]; then
  ln -sf /lib/systemd/system/sshd.service "$WANTS/sshd.service"
elif [ -e "$MNT/usr/lib/systemd/system/ssh.service" ]; then
  ln -sf /usr/lib/systemd/system/ssh.service "$WANTS/ssh.service"
fi

if [ -n "$FIRSTBOOT" ]; then
  [ -f "$FIRSTBOOT" ] || die "--firstboot not found: $FIRSTBOOT"
  [ -d "$MNT/etc/systemd/system" ] || die "target rootfs has no systemd; cannot install first boot"
  install -d "$MNT/usr/local/sbin" "$MNT/etc/systemd/system/multi-user.target.wants"
  install -m 0755 "$FIRSTBOOT" "$MNT/usr/local/sbin/nokeyout-firstboot"
  cat > "$MNT/etc/systemd/system/nokeyout-firstboot.service" <<'EOS'
[Unit]
Description=NoKeyOut first boot install
After=local-fs.target
ConditionPathExists=!/var/lib/nokeyout/.firstboot-done
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/nokeyout-firstboot
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
EOS
  ln -sf ../nokeyout-firstboot.service "$MNT/etc/systemd/system/multi-user.target.wants/nokeyout-firstboot.service"
fi

install -d "$MNT/var/lib/nokeyout"
printf '%s\n' "$LANG_CODE" > "$MNT/var/lib/nokeyout/lang"
if [ -n "$BUNDLE" ]; then
  [ -d "$BUNDLE" ] || die "--bundle not found: $BUNDLE"
  NEED_KB="$(du -sk "$BUNDLE" | awk '{print $1}')"
  FREE_KB="$(df -k "$MNT" | awk 'NR==2{print $4}')"
  [ "$FREE_KB" -gt "$NEED_KB" ] || die "offline bundle needs ${NEED_KB}KB, rootfs has ${FREE_KB}KB free"
  install -d -m 0700 "$MNT/var/lib/nokeyout/firstboot-bundle"
  cp -a "$BUNDLE/." "$MNT/var/lib/nokeyout/firstboot-bundle/"
  chmod -R go-rwx "$MNT/var/lib/nokeyout/firstboot-bundle"
  chmod 0755 "$MNT/var/lib/nokeyout/firstboot-bundle/panel" "$MNT/var/lib/nokeyout/firstboot-bundle/install-nokeyout.sh" 2>/dev/null || true
fi

if [ -n "$BOOT_MNT" ] && [ -n "$BUNDLE" ] && [ -f "$BUNDLE/nokeyout-screen.dtbo" ]; then
  mkdir -p "$BOOT_MNT/overlay-user"
  cp "$BUNDLE/nokeyout-screen.dtbo" "$BOOT_MNT/overlay-user/nokeyout-screen.dtbo"
  ENV="$BOOT_MNT/armbianEnv.txt"
  if grep -q '^user_overlays=' "$ENV" 2>/dev/null; then
    grep -q 'nokeyout-screen' "$ENV" || sed -i 's/^user_overlays=.*/& nokeyout-screen/' "$ENV"
  else
    echo 'user_overlays=nokeyout-screen' >> "$ENV"
  fi
fi

if [ -n "$USER_HOSTNAME" ]; then
  printf '%s\n' "$USER_HOSTNAME" > "$MNT/etc/hostname"
  if [ -f "$MNT/etc/hosts" ]; then
    sed -i -E "s/127\.0\.1\.1[[:space:]]+.*/127.0.1.1\t$USER_HOSTNAME/" "$MNT/etc/hosts"
    grep -q "127.0.1.1" "$MNT/etc/hosts" || printf '127.0.1.1\t%s\n' "$USER_HOSTNAME" >> "$MNT/etc/hosts"
  fi
fi

sync
if [ -n "$BOOT_MNT" ] && mountpoint -q "$BOOT_MNT" 2>/dev/null; then umount "$BOOT_MNT" || true; fi
BOOT_MNT=""
umount "$MNT"
rmdir "$MNT" 2>/dev/null || true
MNT=""

say "Done."
say "User: $USER_NAME"
say "SSH:  enabled (PasswordAuthentication yes)"
say "Eject the card, insert it into the board, power on, then: ssh $USER_NAME@<device-ip>"
