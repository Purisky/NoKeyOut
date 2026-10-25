#!/usr/bin/env bash
# setup.sh — 安装 nokeyout 管理面（panel + switchd 替身）
#
# 前提：magpie 已在设备上运行（端口 3425/3430）。
#
# 用法： sudo ./setup.sh [--ip <device-ip>] [--port 8443] [--container magpie]
#                        [--dns nokeyout.local]… [--cert-reissue] [--dry-run]
#   证书：自签证书，由 device/panel/nokeyout-cert.sh 签发。
#   --dns 追加 SAN 名（需 mDNS/内网 DNS 才可解析）；--cert-reissue 强制重签。
set -euo pipefail
APP=/var/lib/nokeyout/app
ETC=/etc/nokeyout
HERE="$(cd "$(dirname "$0")" && pwd)"
IP="${NOKEYOUT_DEVICE_IP:-192.0.2.10}"; PORT="8443"; CTR="magpie"; DRY=0; CERT_REISSUE=""
DNS_ARGS=()
while [ $# -gt 0 ]; do case "$1" in
  --ip) IP="$2"; shift 2;; --port) PORT="$2"; shift 2;;
  --container) CTR="$2"; shift 2;; --dry-run) DRY=1; shift;;
  --dns) DNS_ARGS+=(--dns "$2"); shift 2;;
  --cert-reissue) CERT_REISSUE="--reissue"; shift;;
  *) echo "未知参数 $1"; exit 1;; esac; done

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
run() { echo "  \$ $*"; [ "$DRY" = 1 ] || eval "$@"; }
[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }

say "1/7 目录与文件"
run "install -d -m 0755 $ETC /var/lib/nokeyout/secrets /var/lib/nokeyout/tls"
run "install -d -m 0755 $APP /var/lib/nokeyout/state"
# 设备侧只运行 Go 静态二进制。
# 二进制缺失即中止——安全组件不做「静默回退到另一套实现」。
[ -s "$HERE/../panel-go/panel" ] || { echo "缺 panel-go/panel（发布前跑 device/panel-go/build.sh）"; exit 1; }
run "install -m 0755 '$HERE/../panel-go/panel' $APP/panel-go"
PANEL_EXEC="$APP/panel-go"
echo "  panel: Go 静态二进制（CGO=0）"
export PANEL_EXEC
# 小屏是**可选组件**（硬件 SPI 屏接好后启用）。默认不装；
# NOKEYOUT_SCREEN=1 时由 Go 版 panel 自己起渲染线程（SCREEN_BACKEND=console|st7789|png），
# 无需单独安装渲染包或字体脚本。
# sysfs GPIO 的 export/direction 要 root：oneshot 单元先导出 DC/RST 并把节点
# chown 给面板用户（panel 非 root 跑，之后只写 value）。NOKEYOUT_SCREEN=0 不装。
if [ "${NOKEYOUT_SCREEN:-0}" = "1" ]; then
  run "cat > /usr/local/bin/nokeyout-screen-gpio" <<'EOS'
#!/bin/sh
# 由 nokeyout-screen-gpio.service 调用：导出屏用 DC/RST 的 sysfs 节点并交给 nokeyout 用户。
# 行号来自 panel.env（SCREEN_DC/SCREEN_RST，-1=不接硬件复位）。失效方向=开放，
# 本步骤失败不阻塞 panel 启动（屏幕退回 console 输出，日志里有原因）。
for line in ${SCREEN_DC:-128} ${SCREEN_RST:-140}; do
  [ "$line" = "-1" ] && continue
  d="/sys/class/gpio/gpio$line"
  [ -d "$d" ] || echo "$line" > /sys/class/gpio/export || continue
  echo out > "$d/direction" || continue
  chmod 666 "$d/direction" "$d/value" 2>/dev/null || true
done
exit 0
EOS
  run "chmod 0755 /usr/local/bin/nokeyout-screen-gpio"
  run "cat > /etc/systemd/system/nokeyout-screen-gpio.service" <<EOF
[Unit]
Description=NoKeyOut screen GPIO (sysfs export for panel user)
DefaultDependencies=no
After=sysinit.target
[Service]
Type=oneshot
RemainAfterExit=yes
EnvironmentFile=-$ETC/panel.env
ExecStart=/usr/local/bin/nokeyout-screen-gpio
[Install]
WantedBy=multi-user.target
EOF
  run "systemctl daemon-reload && systemctl enable nokeyout-screen-gpio.service"
  echo "  screen-gpio: oneshot 已装；屏幕模块在 管理面 → 系统 → 屏幕模块 里选（默认 配额+用量）"
else
  echo "  screen-gpio: 跳过（可选组件，NOKEYOUT_SCREEN=1 开启）"
fi
# USER 按键（板载 K2_USR = GPIO4_B2 = 138）：sysfs 导出为输入并交给面板用户。
# 与屏幕/开关独立——没有屏也应可用。USER_BUTTON_GPIO=-1 关闭。
run "cat > /usr/local/bin/nokeyout-user-button-gpio" <<'EOS'
#!/bin/sh
# 由 nokeyout-user-button-gpio.service 调用：导出 USER 键的 sysfs 节点并交给 nokeyout 用户。
# 行号来自 panel.env（USER_BUTTON_GPIO，-1=不启用）。失败不阻塞 panel 启动。
line="${USER_BUTTON_GPIO:-138}"
[ "$line" = "-1" ] && exit 0
d="/sys/class/gpio/gpio$line"
[ -d "$d" ] || echo "$line" > /sys/class/gpio/export || exit 0
echo in > "$d/direction" || exit 0
chown -R nokeyout:nokeyout "$d" 2>/dev/null || true
exit 0
EOS
run "chmod 0755 /usr/local/bin/nokeyout-user-button-gpio"
run "cat > /etc/systemd/system/nokeyout-user-button-gpio.service" <<EOF
[Unit]
Description=NoKeyOut USER button GPIO (sysfs export for panel user)
DefaultDependencies=no
After=sysinit.target
[Service]
Type=oneshot
RemainAfterExit=yes
EnvironmentFile=-$ETC/panel.env
ExecStart=/usr/local/bin/nokeyout-user-button-gpio
[Install]
WantedBy=multi-user.target
EOF
run "systemctl daemon-reload && systemctl enable nokeyout-user-button-gpio.service"
run "install -m 0755 '$HERE/nokeyout-cert.sh' /usr/local/bin/nokeyout-cert"
run "install -d -m 0755 $APP/web"
run "install -m 0644 $HERE/web/*.html $APP/web/"

say "2/7 会话密钥（会话 cookie 的 HMAC key，0600）"
if [ ! -s /var/lib/nokeyout/secrets/session.key ]; then
  run "openssl rand -hex 32 > /var/lib/nokeyout/secrets/session.key"
  run "chmod 600 /var/lib/nokeyout/secrets/session.key"
else echo "  已存在，跳过"; fi
if [ ! -s /var/lib/nokeyout/secrets/screen.token ]; then
  run "openssl rand -hex 24 > /var/lib/nokeyout/secrets/screen.token"
  run "chmod 600 /var/lib/nokeyout/secrets/screen.token"
else echo "  screen.token 已存在，跳过"; fi

say "3/7 自签 TLS 证书"
run "bash '$HERE/nokeyout-cert.sh' --ip $IP ${CERT_REISSUE} ${DNS_ARGS[*]:-}"

say "4/7 两个服务端凭据"
# 4a magpie web key（控制台代理注入用）。优先级：环境变量 > docker inspect > 留空并告警
WEBKEY="${MAGPIE_WEB_KEY:-}"
if [ -z "$WEBKEY" ] && command -v docker >/dev/null && docker ps --format '{{.Names}}' | grep -qx "$CTR"; then
  WEBKEY="$(docker inspect "$CTR" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^MAGPIE_WEB_KEY=//p' | head -1 || true)"
fi
if [ -n "$WEBKEY" ]; then
  run "printf '%s' '$WEBKEY' > /var/lib/nokeyout/secrets/magpie-web.key"
  run "chmod 600 /var/lib/nokeyout/secrets/magpie-web.key"
  echo "  ✅ MAGPIE_WEB_KEY 已写入服务端保管"
else
  echo "  ⚠️ 未找到 MAGPIE_WEB_KEY：控制台代理将不可用。"
  echo "     修复：docker run 时加 -e MAGPIE_WEB_KEY=\$(openssl rand -hex 16)，"
  echo "     然后 printf '%s' '<key>' > /var/lib/nokeyout/secrets/magpie-web.key && chmod 600 同文件"
fi
# 4b 面板读 magpie 的凭证：
#   二进制模式：panel 与 magpie 同在设备上，走 127.0.0.1 回环，magpie 免鉴权 → 不建 key。
#   docker 模式：宿主→容器不是回环，保留一把面板专用 key（非客户端 key，面板下拉会过滤）。
if systemctl is-active --quiet nokeyout-magpie-binary 2>/dev/null; then
  echo "  ℹ️ 二进制模式：面板走 127.0.0.1 回环读取，不创建网关 key"
elif command -v docker >/dev/null && docker ps --format '{{.Names}}' | grep -qx "$CTR"; then
  if [ ! -s /var/lib/nokeyout/secrets/panel-gateway.key ]; then
    if docker exec "$CTR" magpie gateway-key list 2>/dev/null | grep -q 'nokeyout-panel'; then
      echo "  ⚠️ 已有名为 nokeyout-panel 的网关 key，但未取到明文。"
      echo "     手动：K=\$(docker exec $CTR magpie gateway-key add nokeyout-panel) && printf '%s' \"\$K\" > /var/lib/nokeyout/secrets/panel-gateway.key && chmod 600 同文件"
    else
      K="$(docker exec "$CTR" magpie gateway-key add nokeyout-panel | head -n1 | tr -d '\r')"
      if [ -n "$K" ]; then
        run "printf '%s' '$K' > /var/lib/nokeyout/secrets/panel-gateway.key"
        run "chmod 600 /var/lib/nokeyout/secrets/panel-gateway.key"
        echo "  ✅ 已创建面板专用网关 key nokeyout-panel（docker 模式；客户端下拉不显示）"
      fi
    fi
  else echo "  已存在，跳过"; fi
else
  echo "  ℹ️ 二进制/无容器模式：面板走 127.0.0.1 回环读取，不创建网关 key"
fi

say "5/7 非 root 运行（用户 + PAM/CLI helper + sudoers）"
# panel 以 nokeyout 系统用户运行；两类 root 操作经 sudoers 放行给 helper：
#   nokeyout-auth     —— PAM 校验任意账号（unix_chkpwd 对非 root 只允许本人）
#   nokeyout-magpie-cli —— magpie CLI 白名单（包装器）
#   /usr/bin/chpasswd —— 首启向导改口令
id nokeyout >/dev/null 2>&1 || run "useradd --system --shell /usr/sbin/nologin --home-dir /var/lib/nokeyout nokeyout"
[ -s "$HERE/../gateway/bin/nokeyout-auth" ] || { echo "缺 gateway/bin/nokeyout-auth（跑 device/gateway/build-auth.sh）"; exit 1; }
  run "install -m 0755 '$HERE/../gateway/bin/nokeyout-auth' /usr/local/bin/nokeyout-auth"
  echo "  nokeyout-auth: C 静态二进制"
  run "install -m 0755 '$HERE/../gateway/nokeyout-magpie-cli' /usr/local/bin/nokeyout-magpie-cli"
  run "install -m 0755 '$HERE/../gateway/nokeyout-upgrade' /usr/local/bin/nokeyout-upgrade"
  if [ -s "$HERE/../update/nokeyout-update" ]; then
    run "install -m 0755 '$HERE/../update/nokeyout-update' /usr/local/bin/nokeyout-update"
    echo "  nokeyout-update: 签名发布客户端"
  else
    echo "  nokeyout-update: 未随包（自更新不可用）"
  fi
  if [ -s "$HERE/../update/release.pub" ]; then
    run "install -d -m 0755 /etc/nokeyout"
    run "install -m 0644 '$HERE/../update/release.pub' /etc/nokeyout/release.pub"
    echo "  release.pub: 已安装"
  fi
  run "install -d -m 0755 /usr/local/lib/nokeyout /usr/local/lib/nokeyout/bin"
  # 静态 OCI 直取器：magpie arm64 更新
  if [ -f "$HERE/../tools/magpie-pull/magpie-pull" ]; then
    run "install -m 0755 '$HERE/../tools/magpie-pull/magpie-pull' /usr/local/lib/nokeyout/bin/magpie-pull"
    echo "  magpie-pull: 已装"
  else
    echo "  magpie-pull: 未随包（更新回退容器路径）"
  fi
  run "install -m 0755 '$HERE/../gateway/update-magpie.sh' /usr/local/lib/nokeyout/update-magpie.sh"
  [ -f "$HERE/../gateway/update-magpie-binary.sh" ] && \
    run "install -m 0755 '$HERE/../gateway/update-magpie-binary.sh' /usr/local/lib/nokeyout/update-magpie-binary.sh"
  run "printf 'nokeyout ALL=(root) NOPASSWD: /usr/local/bin/nokeyout-magpie-cli, /usr/bin/chpasswd, /usr/local/bin/nokeyout-auth, /usr/local/bin/nokeyout-upgrade, /usr/local/bin/nokeyout-update\n' > /etc/sudoers.d/nokeyout-panel"
  run "chmod 440 /etc/sudoers.d/nokeyout-panel"
  run "visudo -c | tail -1"
# 只 chown 面板自己的目录——勿 -R /var/lib/nokeyout（magpie-config 归 65532 的
run "chown -R nokeyout:nokeyout /var/lib/nokeyout/app /var/lib/nokeyout/secrets /var/lib/nokeyout/tls /var/lib/nokeyout/state"
# USER 键的 sysfs 节点只在 nokeyout 用户存在后 chown 才有意义 → 放到这里启动
run "systemctl start nokeyout-user-button-gpio.service || true"
run "chown nokeyout:nokeyout /var/lib/nokeyout 2>/dev/null || true"
run "chmod 700 /var/lib/nokeyout/secrets /var/lib/nokeyout/tls"
run "touch $ETC/panel.env; chown root:nokeyout $ETC/panel.env; chmod 640 $ETC/panel.env"

say "6/7 配置与 systemd"
# 登录认证走 C 版 PAM helper（sudo -n nokeyout-auth），**不依赖 python3/pam 模块**。
run "cat > $ETC/panel.env" <<EOF
# nokeyout-panel 配置（setup.sh 生成；改完 systemctl restart nokeyout-panel）
PANEL_PORT=$PORT
PANEL_TLS_CERT=/var/lib/nokeyout/tls/cert.pem
PANEL_TLS_KEY=/var/lib/nokeyout/tls/key.pem
PANEL_SESSION_TTL=1800
GATEWAY_BASE=http://127.0.0.1:3425
UPSTREAM_WEB=http://127.0.0.1:3430
SWITCHD_URL=http://127.0.0.1:8799
PANEL_SECRET_FILE=/var/lib/nokeyout/secrets/session.key
MAGPIE_WEB_KEY_FILE=/var/lib/nokeyout/secrets/magpie-web.key
PANEL_SCREEN_TOKEN_FILE=/var/lib/nokeyout/secrets/screen.token
SWITCHD_STATE_FILE=/var/lib/nokeyout/switch-state
# 物理开关：pin11 = GPIO4_A5 = chip4 line 5；断路（上拉）= LOCKED，闭合（GND）= ADMIN
NOKEYOUT_SWITCH_CHIP=gpiochip4
NOKEYOUT_SWITCH_LINE=5
# 屏幕（可选；渲染线程在 Go panel 内，console/st7789/png 三后端）
# SCREEN_BACKEND=console|st7789|png 才会启用；空=不渲染。接好后：NOKEYOUT_SCREEN=1 重跑 setup.sh。
# SCREEN_BACKEND=st7789
# SCREEN_SPI=/dev/spidev1.1
# SCREEN_DC=128
# SCREEN_RST=140
# SCREEN_MADCTL=0x70
# SCREEN_INVERT=1
# SCREEN_SELFTEST=1
# 用户自定义屏页（JSON 描述符；损坏自动跳过，不影响内置三页）
SCREENS_DIR=/var/lib/nokeyout/screens
# USER 按键（板载 K2_USR = GPIO4_B2 = 138；按下短接到 GND）。
# 默认动作=熄屏/亮屏；动作可在 管理面 → 系统 → 用户按键 改（USER_BUTTON_FILE）。
# -1 = 不启用。屏幕开关状态只存内存，重启/上电一律亮屏。
USER_BUTTON_GPIO=138
USER_BUTTON_ACTIVE_LOW=1
USER_BUTTON_POLL_MS=30
USER_BUTTON_DEBOUNCE_MS=60
USER_BUTTON_FILE=/var/lib/nokeyout/state/user-button.json
EOF
DOCKER_AFTER=""; DOCKER_WANTS=""
if command -v docker >/dev/null 2>&1; then
  DOCKER_AFTER=" docker.service"; DOCKER_WANTS="Wants=docker.service"
fi
run "cat > /etc/systemd/system/nokeyout-panel.service" <<EOF
[Unit]
Description=NoKeyOut management panel
After=network.target$DOCKER_AFTER nokeyout-screen-gpio.service nokeyout-user-button-gpio.service
$DOCKER_WANTS
[Service]
Type=simple
User=nokeyout
Group=nokeyout
ExecStart=$PANEL_EXEC
EnvironmentFile=-$ETC/panel.env
Restart=on-failure
RestartSec=2
# 非 root 运行：PAM/CLI 两类 root 操作经 /etc/sudoers.d/nokeyout-panel 放行给 helper
[Install]
WantedBy=multi-user.target
EOF
systemctl disable --now nokeyout-switchd >/dev/null 2>&1 || true
rm -f /etc/systemd/system/nokeyout-switchd.service
# 清理旧版 :3426 密钥前缀代理的单元、二进制与密钥映射。
# 网关密钥的唯一来源是 magpie 的 caller-keys.json。
systemctl disable --now nokeyout-key-proxy >/dev/null 2>&1 || true
rm -f /etc/systemd/system/nokeyout-key-proxy.service \
      /usr/local/lib/nokeyout/bin/keyproxy \
      /var/lib/nokeyout/secrets/key-map.json \
      /var/lib/nokeyout/secrets/proxy-admin.token
run "systemctl daemon-reload"
run "systemctl enable nokeyout-panel"
run "systemctl restart nokeyout-panel"

say "6b/7 物理开关门控"
# 开关断路（LOCK）必须让「泄漏口令 + SSH」这条路也断掉：只关 :8443 不够。
# 装完即生效——开关在 LOCK 位时，本安装脚本自己的 SSH 会话也会被终止。
echo "  ⚠️ 开关在 LOCK 位时，安装完成会立即停掉 SSH（含当前会话）；请先在 ADMIN 位安装"
run "install -m 0755 '$HERE/nokeyout-switchd' /usr/local/bin/nokeyout-switchd"
run "install -m 0755 '$HERE/nokeyout-gate-check' /usr/local/bin/nokeyout-gate-check"
run "install -m 0644 '$HERE/systemd/nokeyout-switch.service' /etc/systemd/system/nokeyout-switch.service"
run "install -d -m 0755 /etc/systemd/system/ssh.service.d"
run "install -m 0644 '$HERE/systemd/ssh.service.d/nokeyout-gate.conf' /etc/systemd/system/ssh.service.d/nokeyout-gate.conf"
# 上拉必须在设备树里配：rockchip gpio 驱动忽略运行时 bias 请求，
# 没有它时「断路（LOCK）」与「闭合（ADMIN）」都读 0，门控永远不触发。
NEED_REBOOT=0
if command -v dtc >/dev/null 2>&1 && [ -d /boot/overlay-user ]; then
  run "dtc -@ -q -I dts -O dtb -o /boot/overlay-user/nokeyout-gate.dtbo '$HERE/overlays/nokeyout-gate.dts'"
  if ! grep -q 'nokeyout-gate' /boot/armbianEnv.txt 2>/dev/null; then
    if grep -q '^user_overlays=' /boot/armbianEnv.txt 2>/dev/null; then
      run "sed -i 's/^user_overlays=.*/& nokeyout-gate/' /boot/armbianEnv.txt"
    else
      run "echo 'user_overlays=nokeyout-gate' >> /boot/armbianEnv.txt"
    fi
    NEED_REBOOT=1
  fi
else
  echo "  ⚠️ 缺 dtc 或 /boot/overlay-user：未装门控上拉，开关读数不可信"
fi
run "systemctl daemon-reload"
run "systemctl enable nokeyout-switch.service"
run "systemctl restart nokeyout-switch.service"
[ "${NEED_REBOOT:-0}" = 1 ] && echo "  ⚠️ 设备树门控上拉是新加的：执行 reboot 后才生效"
echo "  switch: LOCK ⇒ 停 ssh + 踢会话 + 关 :8443；ADMIN ⇒ 恢复"


say "7/7 自检"
up=0
for i in $(seq 1 20); do
  if curl -fsSk "https://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then up=1; break; fi
  sleep 1
done
[ "$up" = 1 ] || { echo "  ❌ $PORT 自检失败（20s 未就绪）"; exit 1; }
run "curl -fsSk https://127.0.0.1:$PORT/healthz"
echo
echo "管理面:  https://$IP:$PORT/"
echo "浏览器首次访问会提示自签证书不受信任。"
echo "HELPER:  sudo nokeyout-cert --print | --reissue | --ip <新IP>（IP 变更/续签/重签）"
echo "端口表（收敛后）：3425 网关(LAN) / $PORT 管理面 / 22 ssh / 3430 仅回环 / 3000·80 关闭（443 为出网代理，勿动）"
