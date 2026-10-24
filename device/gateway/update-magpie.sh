#!/usr/bin/env bash
# update-magpie.sh —— 锁版升级 + 冒烟 + 失败自动回滚
#
# 设计前提：magpie 更新频繁（上游活跃），设备必须「可升、可回滚、升完能自证」。
# 本脚本从*正在运行的容器*反推运行参数（env/卷/端口/命令），只替换镜像 tag，
# 避免手写 docker run 与现网漂移。
#
#   sudo ./update-magpie.sh 0.1.1180            # 升级到指定版本
#   sudo ./update-magpie.sh 0.1.1179 --dry-run  # 同版本空跑（验证流程，不动版本）
#
# 步骤：记录回滚点 → pull（失败即弃，现网不动）→ 重建 → 健康等待 →
#       冒烟（/v1/models 200 + 临时 key 真调一次）→ 失败自动回滚并报告。
set -euo pipefail
CTR="${CTR:-magpie}"
REG="${REG:-ghcr.io/yetone/magpie}"
NEWVER="${1:-}"; DRY=0; NOPULL=0
for a in "$@"; do [ "$a" = "--dry-run" ] && DRY=1; [ "$a" = "--no-pull" ] && NOPULL=1; done
[ -n "$NEWVER" ] || { echo "用法: $0 <version> [--dry-run] [--no-pull]"; exit 1; }
say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }

say "1/6 记录回滚点"
docker inspect "$CTR" >/dev/null 2>&1 || { echo "容器 $CTR 不存在"; exit 1; }
OLD_IMG=$(docker inspect "$CTR" --format '{{.Config.Image}}')
RESTART=$(docker inspect "$CTR" --format '{{.HostConfig.RestartPolicy.Name}}')
CMD_JSON=$(docker inspect "$CTR" --format '{{json .Config.Cmd}}')
BINDS=$(docker inspect "$CTR" --format '{{range .HostConfig.Binds}}{{println .}}{{end}}')
ENVS=$(docker inspect "$CTR" --format '{{range .Config.Env}}{{println .}}{{end}}')
# 端口：从 inspect JSON 用 sed 解析（docker port 会把同一绑定拆成 v4/v6 两行，反推会重复）；
# 3430 必须只见回环，否则拒绝升级（防止顺带裸奔）。解析结果为空 = 拒绝执行
PB=$(docker inspect "$CTR" --format '{{json .HostConfig.PortBindings}}')
PORTS=()
for e in $(printf '%s' "$PB" | grep -o '"[0-9]*/tcp":\[{[^}]*}]'); do
  cport=$(printf '%s' "$e" | sed -E 's|^"([0-9]+)/tcp.*|\1|')
  hip=$(printf '%s' "$e" | sed -nE 's/.*"HostIp":"([^"]*)".*/\1/p')
  hp=$(printf '%s' "$e" | sed -nE 's/.*"HostPort":"([^"]*)".*/\1/p')
  [ -n "$cport" ] && [ -n "$hp" ] || continue
  if [ "$cport" = "3430" ] && [ "$hip" != "127.0.0.1" ]; then
    echo "❌ 3430 绑定为 '${hip:-0.0.0.0}'（非回环）——先收敛再升级"; exit 1
  fi
  PORTS+=("${hip:-0.0.0.0}:${hp}:${cport}")
done
if [ "${#PORTS[@]}" -eq 0 ]; then
  echo "❌ 端口解析为空（$PB）——拒绝重建（防止重建出零发布端口容器）"; exit 1
fi
PORT_ARGS=()
for p in "${PORTS[@]}"; do PORT_ARGS+=(-p "$p"); done
echo "  回滚镜像: $OLD_IMG   端口: ${PORTS[*]}"
BAK=/var/backups/nokeyout/update-$(date +%Y%m%d-%H%M%S)
install -d -m 700 "$BAK"
docker inspect "$CTR" > "$BAK/inspect.json"
printf '%s\n' "$OLD_IMG" > "$BAK/image.txt"
echo "  回滚点: $BAK"

NEW_IMG="$REG:$NEWVER"
say "2/6 pull $NEW_IMG（失败不动现网）"
if [ "$NOPULL" = 1 ]; then
  docker image inspect "$NEW_IMG" >/dev/null 2>&1 || { echo "  ❌ --no-pull 但本地没有 $NEW_IMG"; exit 1; }
  echo "  --no-pull：使用本地镜像 $(docker image inspect "$NEW_IMG" --format '{{.Id}}' | cut -c8-19)"
elif [ "$DRY" = 1 ] && [ "$NEW_IMG" = "$OLD_IMG" ]; then
  echo "  --dry-run 且版本相同：仅验证流程参数，最后一步仍会重建容器（同镜像）"
else
  docker pull "$NEW_IMG" | tail -1
fi

say "3/6 重建容器"
CMD_ARGS=()
if [ "$CMD_JSON" != "null" ] && [ "$CMD_JSON" != "[]" ]; then
  # JSON 字符串数组 → 每行一个元素（剥引号即可，CMD 元素不含转义引号）
  while read -r a; do CMD_ARGS+=("$a"); done < <(printf '%s' "$CMD_JSON" | grep -oE '"[^"]+"' | tr -d '"')
fi
ENV_ARGS=()
while read -r e; do [ -n "$e" ] && ENV_ARGS+=(-e "$e"); done <<<"$ENVS"
BIND_ARGS=()
while read -r b; do [ -n "$b" ] && BIND_ARGS+=(-v "$b"); done <<<"$BINDS"
[ "${#ENV_ARGS[@]}" -gt 0 ] && [ "${#BIND_ARGS[@]}" -gt 0 ] || {
  echo "❌ env/卷解析异常（ENV=${#ENV_ARGS[@]} BIND=${#BIND_ARGS[@]}）——拒绝重建"; exit 1; }
HEALTH_OK=0
rollback() {
  say "↩ 回滚到 $OLD_IMG"
  docker rm -f "$CTR" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run -d --name "$CTR" --restart "$RESTART" ${PORT_ARGS[@]+"${PORT_ARGS[@]}"} \
    ${ENV_ARGS[@]+"${ENV_ARGS[@]}"} ${BIND_ARGS[@]+"${BIND_ARGS[@]}"} \
    "$OLD_IMG" ${CMD_ARGS[@]+"${CMD_ARGS[@]}"} >/dev/null
  echo "  已回滚。日志: $BAK"
}
docker rm -f "$CTR" >/dev/null
# shellcheck disable=SC2086
docker run -d --name "$CTR" --restart "$RESTART" ${PORT_ARGS[@]+"${PORT_ARGS[@]}"} \
  ${ENV_ARGS[@]+"${ENV_ARGS[@]}"} ${BIND_ARGS[@]+"${BIND_ARGS[@]}"} \
  "$NEW_IMG" ${CMD_ARGS[@]+"${CMD_ARGS[@]}"} >/dev/null
sleep 4
if [ -z "$(docker port "$CTR" 2>/dev/null)" ]; then
  echo "  ❌ 新容器零发布端口"; rollback; exit 1
fi
docker port "$CTR" | sed 's/^/    /'

say "4/6 健康等待（最多 60s）"
for i in $(seq 1 30); do
  if docker exec "$CTR" magpie healthcheck >/dev/null 2>&1; then HEALTH_OK=1; break; fi
  sleep 2
done
if [ "$HEALTH_OK" != 1 ]; then echo "  ❌ healthcheck 未过"; rollback; exit 1; fi
echo "  ✅ healthcheck"

say "5/6 冒烟（/v1/models + 一次真实调用）"
PK=/var/lib/nokeyout/secrets/panel-gateway.key
[ -s "$PK" ] || { echo "  ❌ 缺 panel key"; rollback; exit 1; }
KEY=$(cat "$PK")
CODE=$(curl -s -o /dev/null -m 10 -w '%{http_code}' -H "Authorization: Bearer $KEY" http://127.0.0.1:3425/v1/models)
[ "$CODE" = 200 ] || { echo "  ❌ /v1/models -> $CODE"; rollback; exit 1; }
echo "  ✅ /v1/models 200"
MODEL=$(curl -s -m 10 -H "Authorization: Bearer $KEY" http://127.0.0.1:3425/v1/models \
        | grep -oE '"id":"[^"]+"' | head -1 | cut -d'"' -f4)
if [ -n "$MODEL" ]; then
  CODE=$(curl -s -o /dev/null -m 60 -w '%{http_code}' -H "Authorization: Bearer $KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_tokens\":4}" \
    http://127.0.0.1:3425/v1/chat/completions)
  [ "$CODE" = 200 ] || { echo "  ❌ 真实调用 -> $CODE（模型 $MODEL）"; rollback; exit 1; }
  echo "  ✅ 真实调用 200（$MODEL，4 tokens）"
fi

say "6/7 更新锁版记录"
sed -i "s|image: ghcr.io/yetone/magpie:[0-9.]*|image: $NEW_IMG|" \
  "$(dirname "$0")/magpie-compose.yml" 2>/dev/null || true
echo "  compose 已指向 $NEW_IMG"
say "升级完成 ✅  $OLD_IMG → $NEW_IMG"
