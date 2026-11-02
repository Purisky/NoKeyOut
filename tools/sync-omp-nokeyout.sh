#!/usr/bin/env bash
# sync-omp-nokeyout.sh —— 把本机的 omp nokeyout 配置同步到另一台设备（热同步，不重启任何服务）
#
#   bash tools/sync-omp-nokeyout.sh --host PuriskyDesktop
#   bash tools/sync-omp-nokeyout.sh --host root@192.168.0.110 --base-url http://127.0.0.1:3425/v1
#
# 同步内容：nokeyout-provider.json（网关地址 + 网关密钥）、config.yml、models.yml、
#           ~/.omp/.env、合并 provider API key 凭据、omp-nokeyout-provider 插件。
#
# 安全约定：
#   * 只写 ~/.omp 下的文件，绝不重启 omp / magpie / 网关；正在运行的会话不受影响，
#     新会话才加载新配置。
#   * 目标文件先写 *.omp-sync.tmp，再同目录 mv 原子替换；覆盖前自动备份到
#     ~/.omp/backup-<时间戳>/。
#   * 默认以本机 ~/.omp/agent/nokeyout-provider.json 里的 baseUrl 为目标地址；
#     在网关设备自身上同步时用 --base-url http://127.0.0.1:3425/v1 走环回。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOME="${HOME:-$(cd ~ && pwd)}"
PLUGIN_SRC="$ROOT/omp-provider"
SRC="$HOME/.omp/agent"
HOST=""
BASE_URL=""
SKIP_CREDS=0
SKIP_VERIFY=0
DRY=0

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
die() { echo "sync-omp-nokeyout: $*" >&2; exit 1; }
usage() { cat <<'EOF'
Usage: bash tools/sync-omp-nokeyout.sh --host <ssh-target> [options]

  --host TARGET        ssh 目标（~/.ssh/config 别名或 user@ip），必填
  --base-url URL       写入目标机的网关地址（默认沿用本机配置）
  --skip-credentials   不导入 provider API key 凭据（只同步配置与插件）
  --skip-verify        不做一次 print 模式模型调用自检
  --dry-run            只打印将要执行的动作
  -h, --help           显示帮助
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="${2:?}"; shift 2 ;;
    --base-url) BASE_URL="${2:?}"; shift 2 ;;
    --skip-credentials) SKIP_CREDS=1; shift ;;
    --skip-verify) SKIP_VERIFY=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[ -n "$HOST" ] || { usage; exit 1; }
[ -f "$SRC/nokeyout-provider.json" ] || die "本机缺少 $SRC/nokeyout-provider.json"
[ -f "$SRC/config.yml" ] || die "本机缺少 $SRC/config.yml"
[ -f "$PLUGIN_SRC/nokeyout-provider.ts" ] || die "缺少插件源 $PLUGIN_SRC/nokeyout-provider.ts"

GATEWAY_KEY="$(python -c 'import json,sys;print(json.load(open(sys.argv[1]))["gatewayKey"])' "$SRC/nokeyout-provider.json")"
[ -n "$BASE_URL" ] || BASE_URL="$(python -c 'import json,sys;print(json.load(open(sys.argv[1]))["baseUrl"])' "$SRC/nokeyout-provider.json")"
BASE_URL="${BASE_URL%/}"

say "目标 $HOST"
say "网关 $BASE_URL"
ssh -o ConnectTimeout=10 -o BatchMode=yes "$HOST" true || die "无法 ssh 到 $HOST（端口关闭/密钥不匹配？NET 锁定时 22 会关闭）"

REMOTE_OMP="$(ssh "$HOST" bash -s <<'EOF'
for c in omp "$HOME/.bun/bin/omp" /usr/local/bin/omp /opt/homebrew/bin/omp; do
  if command -v "$c" >/dev/null 2>&1; then command -v "$c"; exit 0; fi
done
exit 1
EOF
)" || die "目标机没有可执行的 omp"
say "omp $REMOTE_OMP"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
python - "$STAGE" "$SRC/nokeyout-provider.json" "$BASE_URL" "$GATEWAY_KEY" <<'EOF'
import json, os, sys
stage, src, base, key = sys.argv[1:5]
d = json.load(open(src))
d["baseUrl"] = base + "/v1" if not base.endswith("/v1") else base
d["gatewayKey"] = key
d.setdefault("providerId", "nokeyout")
with open(os.path.join(stage, "nokeyout-provider.json"), "w", encoding="utf-8") as f:
    json.dump(d, f, ensure_ascii=False)
EOF
cp "$SRC/config.yml" "$STAGE/config.yml"
[ -f "$SRC/models.yml" ] && cp "$SRC/models.yml" "$STAGE/models.yml"
[ -f "$SRC/.env" ] && cp "$SRC/.env" "$STAGE/agent.env"
[ -f "$HOME/.omp/.env" ] && cp "$HOME/.omp/.env" "$STAGE/home.env"
cp "$PLUGIN_SRC/nokeyout-provider.ts" "$STAGE/nokeyout-provider.ts"
cp "$PLUGIN_SRC/package.json" "$STAGE/plugin-package.json"

if [ "$DRY" = 1 ]; then
  say "dry-run：将写入的远端路径"
  cat <<EOF
  ~/.omp/agent/nokeyout-provider.json      (0600)
  ~/.omp/agent/config.yml
  ~/.omp/agent/models.yml
  ~/.omp/agent/.env                        (0600, 若本机存在)
  ~/.omp/.env                              (0600, 若本机存在)
  ~/.omp/plugins/node_modules/omp-nokeyout-provider/{package.json,nokeyout-provider.ts}
EOF
  [ "$SKIP_CREDS" = 0 ] && echo "  ~/.omp/agent/agent.db  ← omp auth import（仅 api_key 类型）"
  [ "$SKIP_VERIFY" = 0 ] && echo "  自检：$REMOTE_OMP -p ... --model nokeyout/opendesign/deepseek-v4.1-flash"
  echo "  不重启任何服务"
  exit 0
fi

say "备份远端配置"
ssh "$HOST" bash -s <<'EOF'
set -eu
B="$HOME/.omp/backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$B/agent" "$B/plugins"
for f in config.yml models.yml nokeyout-provider.json .env; do
  [ -f "$HOME/.omp/agent/$f" ] && cp -a "$HOME/.omp/agent/$f" "$B/agent/" || true
done
[ -f "$HOME/.omp/.env" ] && cp -a "$HOME/.omp/.env" "$B/home.env" || true
[ -d "$HOME/.omp/plugins/node_modules/omp-nokeyout-provider" ] && \
  cp -a "$HOME/.omp/plugins/node_modules/omp-nokeyout-provider" "$B/plugins/" || true
echo "backup=$B"
EOF

say "写入配置（临时文件 → 原子 mv）"
scp -q "$STAGE/nokeyout-provider.json" "$HOST:.omp/agent/.nokeyout-provider.json.omp-sync.tmp"
scp -q "$STAGE/config.yml" "$HOST:.omp/agent/.config.yml.omp-sync.tmp"
[ -f "$STAGE/models.yml" ] && scp -q "$STAGE/models.yml" "$HOST:.omp/agent/.models.yml.omp-sync.tmp"
[ -f "$STAGE/agent.env" ] && scp -q "$STAGE/agent.env" "$HOST:.omp/agent/.env.omp-sync.tmp"
[ -f "$STAGE/home.env" ] && scp -q "$STAGE/home.env" "$HOST:.omp/.env.omp-sync.tmp"
ssh "$HOST" 'mkdir -p ~/.omp/plugins/node_modules/omp-nokeyout-provider'
scp -q "$STAGE/nokeyout-provider.ts" "$HOST:.omp/plugins/node_modules/omp-nokeyout-provider/.nokeyout-provider.ts.omp-sync.tmp"
scp -q "$STAGE/plugin-package.json" "$HOST:.omp/plugins/node_modules/omp-nokeyout-provider/.package.json.omp-sync.tmp"

ssh "$HOST" bash -s <<'EOF'
set -eu
A="$HOME/.omp/agent"
mv -f "$A/.nokeyout-provider.json.omp-sync.tmp" "$A/nokeyout-provider.json"
mv -f "$A/.config.yml.omp-sync.tmp" "$A/config.yml"
[ -f "$A/.models.yml.omp-sync.tmp" ] && mv -f "$A/.models.yml.omp-sync.tmp" "$A/models.yml" || true
[ -f "$A/.env.omp-sync.tmp" ] && mv -f "$A/.env.omp-sync.tmp" "$A/.env" || true
[ -f "$HOME/.omp/.env.omp-sync.tmp" ] && mv -f "$HOME/.omp/.env.omp-sync.tmp" "$HOME/.omp/.env" || true
P="$HOME/.omp/plugins/node_modules/omp-nokeyout-provider"
mv -f "$P/.package.json.omp-sync.tmp" "$P/package.json"
mv -f "$P/.nokeyout-provider.ts.omp-sync.tmp" "$P/nokeyout-provider.ts"
chmod 600 "$A/nokeyout-provider.json" 2>/dev/null || true
[ -f "$A/.env" ] && chmod 600 "$A/.env" || true
[ -f "$HOME/.omp/.env" ] && chmod 600 "$HOME/.omp/.env" || true
EOF

if [ "$SKIP_CREDS" = 0 ]; then
  command -v omp >/dev/null 2>&1 || die "本机没有 omp，无法导出凭据（用 --skip-credentials 跳过）"
  say "合并 provider API key 凭据"
  omp auth export 2>/dev/null | python -c '
import json, sys
d = json.load(sys.stdin)
d["credentials"] = [c for c in d.get("credentials", [])
                    if c.get("credential", {}).get("type") == "api_key"]
json.dump(d, sys.stdout)
' > "$STAGE/creds.json"
  ssh "$HOST" 'export PATH="$HOME/.bun/bin:$PATH"; omp auth import -' < "$STAGE/creds.json"
fi

if [ "$SKIP_VERIFY" = 0 ]; then
  say "自检（print 模式，一次网关调用）"
  ssh "$HOST" "PATH=\$HOME/.bun/bin:\$PATH $REMOTE_OMP -p 'Reply with exactly: SYNC-OK' --model nokeyout/opendesign/deepseek-v4.1-flash --auto-approve --no-session </dev/null" 2>&1 | tail -5
  echo
  echo "自检完成：输出含 SYNC-OK 即代表目标机经网关可用。"
fi

say "完成（未重启任何服务；新会话生效）"
