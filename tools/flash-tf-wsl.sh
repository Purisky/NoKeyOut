#!/usr/bin/env bash
# flash-tf-wsl.sh - prepare an ext4 image before Windows writes it to a TF card.
#
# Runs inside WSL2 as root. Loop-mounts the image, creates the `nokeyout` user,
# enables SSH, and installs a one-shot first-boot installer.
#
#   bash flash-tf-wsl.sh <image> <password-hash> [user] [hostname]
#
# The password hash comes from: openssl passwd -6 -stdin
set -euo pipefail

IMG="${1:?image path}"
HASH_ARG="${2:?password hash}"
if [ -f "$HASH_ARG" ]; then
  HASH="$(tr -d '\r\n' < "$HASH_ARG")"
else
  HASH="$HASH_ARG"
fi
USER_NAME="${3:-nokeyout}"
USER_HOSTNAME="${4:-nokeyout}"
LANG_CODE="${5:-en}"
BUNDLE="${6:-}"
[ "$LANG_CODE" = "zh" ] || LANG_CODE=en

command -v losetup >/dev/null 2>&1 || { echo "missing losetup" >&2; exit 1; }
command -v lsblk >/dev/null 2>&1 || { echo "missing lsblk" >&2; exit 1; }
command -v mount >/dev/null 2>&1 || { echo "missing mount" >&2; exit 1; }
case "$IMG" in
  /*) ;;
  *) IMG="$(wslpath -a "$IMG")" ;;
esac
[ -f "$IMG" ] || { echo "image not found: $IMG" >&2; exit 1; }

LOOP=""
MNT=""
BOOT_MNT=""
cleanup() {
  if [ -n "$BOOT_MNT" ] && mountpoint -q "$BOOT_MNT" 2>/dev/null; then umount "$BOOT_MNT" || true; fi
  if [ -n "$MNT" ] && mountpoint -q "$MNT" 2>/dev/null; then umount "$MNT" || true; fi
  if [ -n "$LOOP" ]; then losetup -d "$LOOP" 2>/dev/null || true; fi
  [ -n "$MNT" ] && rmdir "$MNT" 2>/dev/null || true
}
trap cleanup EXIT

LOOP="$(losetup --find --show -Pf "$IMG")"
for _ in $(seq 1 25); do
  ROOT="$(lsblk -b -rno NAME,FSTYPE,SIZE "$LOOP" 2>/dev/null | awk '$2=="ext4"{print $3, "/dev/"$1}' | sort -nr | head -1 | awk '{print $2}')"
  [ -n "$ROOT" ] && [ -b "$ROOT" ] && break
  sleep 0.2
done
[ -n "$ROOT" ] && [ -b "$ROOT" ] || { echo "no ext4 rootfs in $IMG" >&2; exit 1; }

MNT="$(mktemp -d)"
mount -o rw "$ROOT" "$MNT"

BOOT_PART="$(lsblk -rno NAME,LABEL,FSTYPE "$LOOP" | awk '$2=="armbi_boot" && $3=="ext4"{print "/dev/"$1; exit}')"
if [ -z "$BOOT_PART" ]; then
  BOOT_PART="$(lsblk -b -rno NAME,FSTYPE,SIZE "$LOOP" | awk '$2=="ext4"{print $3, "/dev/"$1}' | sort -n | head -1 | awk '{print $2}')"
fi
if [ -n "$BOOT_PART" ] && [ -b "$BOOT_PART" ]; then
  mkdir -p "$MNT/boot"
  mount -o rw "$BOOT_PART" "$MNT/boot"
  BOOT_MNT="$MNT/boot"
fi

PASSWD="$MNT/etc/passwd"
SHADOW="$MNT/etc/shadow"
GROUP="$MNT/etc/group"
[ -f "$PASSWD" ] && [ -f "$GROUP" ] && [ -f "$SHADOW" ] || {
  echo "not a Linux rootfs: missing /etc/passwd, /etc/group, or /etc/shadow" >&2
  exit 1
}

if grep -q "^$USER_NAME:" "$PASSWD"; then
  USER_ID="$(awk -F: -v u="$USER_NAME" '$1==u{print $3}' "$PASSWD")"
  GROUP_ID="$(awk -F: -v u="$USER_NAME" '$1==u{print $4}' "$PASSWD")"
else
  USER_ID=1000
  while awk -F: -v id="$USER_ID" '$3==id{found=1} END{exit !found}' "$PASSWD"; do USER_ID=$((USER_ID + 1)); done
  GROUP_ID=1000
  while awk -F: -v id="$GROUP_ID" '$3==id{found=1} END{exit !found}' "$GROUP"; do GROUP_ID=$((GROUP_ID + 1)); done
  SHELL_BIN="/bin/bash"
  [ -x "$MNT/bin/bash" ] || SHELL_BIN="/bin/sh"
  printf '%s:x:%s:%s:%s:/home/%s:%s\n' "$USER_NAME" "$USER_ID" "$GROUP_ID" "$USER_NAME" "$USER_NAME" "$SHELL_BIN" >> "$PASSWD"
  printf '%s:x:%s:\n' "$USER_NAME" "$GROUP_ID" >> "$GROUP"
fi

if grep -q "^$USER_NAME:" "$SHADOW"; then
  TODAY=$(( $(date +%s) / 86400 ))
  awk -F: -v u="$USER_NAME" -v h="$HASH" -v d="$TODAY" 'BEGIN{OFS=":"} $1==u{$2=h;$3=d;$7="";$8=""} {print}' "$SHADOW" > "$SHADOW.tmp"
  mv "$SHADOW.tmp" "$SHADOW"
else
  TODAY=$(( $(date +%s) / 86400 ))
  printf '%s:%s:%s:0:99999:7:::\n' "$USER_NAME" "$HASH" "$TODAY" >> "$SHADOW"
fi
chmod 640 "$SHADOW" 2>/dev/null || true

for grp in sudo wheel; do
  if grep -q "^$grp:" "$GROUP"; then
    awk -F: -v OFS=: -v u="$USER_NAME" -v g="$grp" '
      $1==g { if ($4=="") $4=u; else if (index(","$4",", ","u",")==0) $4=$4","u }
      { print }' "$GROUP" > "$GROUP.tmp"
    mv "$GROUP.tmp" "$GROUP"
    break
  fi
done

install -d -m 700 "$MNT/home/$USER_NAME" "$MNT/home/$USER_NAME/.ssh" 2>/dev/null || {
  mkdir -p "$MNT/home/$USER_NAME/.ssh"
  chmod 700 "$MNT/home/$USER_NAME" "$MNT/home/$USER_NAME/.ssh"
}
: > "$MNT/home/$USER_NAME/.ssh/authorized_keys"
chmod 600 "$MNT/home/$USER_NAME/.ssh/authorized_keys"
chown -R "$USER_ID:$GROUP_ID" "$MNT/home/$USER_NAME"

SSHD="$MNT/etc/ssh/sshd_config"
if [ -f "$SSHD" ]; then
  sed -i -E 's/^[#[:space:]]*PasswordAuthentication .*/PasswordAuthentication yes/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*PubkeyAuthentication .*/PubkeyAuthentication yes/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*PermitRootLogin .*/PermitRootLogin no/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*KbdInteractiveAuthentication .*/KbdInteractiveAuthentication yes/' "$SSHD"
  sed -i -E 's/^[#[:space:]]*UsePAM .*/UsePAM yes/' "$SSHD"
  grep -q '^PasswordAuthentication yes' "$SSHD" || echo 'PasswordAuthentication yes' >> "$SSHD"
  grep -q '^PubkeyAuthentication yes' "$SSHD" || echo 'PubkeyAuthentication yes' >> "$SSHD"
  grep -q '^PermitRootLogin no' "$SSHD" || echo 'PermitRootLogin no' >> "$SSHD"
  grep -q '^KbdInteractiveAuthentication yes' "$SSHD" || echo 'KbdInteractiveAuthentication yes' >> "$SSHD"
  grep -q '^UsePAM yes' "$SSHD" || echo 'UsePAM yes' >> "$SSHD"
fi

WANTS="$MNT/etc/systemd/system/multi-user.target.wants"
mkdir -p "$WANTS"
if [ -e "$MNT/lib/systemd/system/ssh.service" ]; then
  ln -sf /lib/systemd/system/ssh.service "$WANTS/ssh.service"
elif [ -e "$MNT/usr/lib/systemd/system/ssh.service" ]; then
  ln -sf /usr/lib/systemd/system/ssh.service "$WANTS/ssh.service"
elif [ -e "$MNT/lib/systemd/system/sshd.service" ]; then
  ln -sf /lib/systemd/system/sshd.service "$WANTS/sshd.service"
elif [ -e "$MNT/usr/lib/systemd/system/sshd.service" ]; then
  ln -sf /usr/lib/systemd/system/sshd.service "$WANTS/sshd.service"
fi

install -d "$MNT/usr/local/sbin" "$WANTS"
cat > "$MNT/usr/local/sbin/nokeyout-firstboot" <<'EOS'
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
  sed -i 's/^# SCREEN_MADCTL=.*/SCREEN_MADCTL=0x70/' /etc/nokeyout/panel.env 2>/dev/null || true
  sed -i 's/^# SCREEN_INVERT=.*/SCREEN_INVERT=1/' /etc/nokeyout/panel.env 2>/dev/null || true
  grep -q '^SCREEN_BACKEND=' /etc/nokeyout/panel.env || echo 'SCREEN_BACKEND=st7789' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_SPI=' /etc/nokeyout/panel.env || echo "SCREEN_SPI=$SPI_DEV" >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_DC=' /etc/nokeyout/panel.env || echo 'SCREEN_DC=128' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_RST=' /etc/nokeyout/panel.env || echo 'SCREEN_RST=140' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_MADCTL=' /etc/nokeyout/panel.env || echo 'SCREEN_MADCTL=0x70' >> /etc/nokeyout/panel.env
  grep -q '^SCREEN_INVERT=' /etc/nokeyout/panel.env || echo 'SCREEN_INVERT=1' >> /etc/nokeyout/panel.env
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
chmod 0755 "$MNT/usr/local/sbin/nokeyout-firstboot"

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
ln -sf ../nokeyout-firstboot.service "$WANTS/nokeyout-firstboot.service"

install -d "$MNT/var/lib/nokeyout"
printf '%s\n' "$LANG_CODE" > "$MNT/var/lib/nokeyout/lang"
printf 'unlocked\n' > "$MNT/var/lib/nokeyout/switch-state"
if [ -n "$BUNDLE" ]; then
  case "$BUNDLE" in /*) ;; *) BUNDLE="$(wslpath -a "$BUNDLE")" ;; esac
  [ -d "$BUNDLE" ] || { echo "bundle not found: $BUNDLE" >&2; exit 1; }
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
echo "prepared: user=$USER_NAME host=$USER_HOSTNAME"
