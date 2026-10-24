#!/usr/bin/env bash
# verify-magpie.sh — magpie 作为 LAN 网关的软件能力验证（无需任何真实 Key）
#   1 无密钥 -> 401
#   2 密钥A 用自己供应商的模型（OpenAI 协议）      -> 200
#   3 密钥A 请求另一供应商的模型                    -> 403（作用域）
#   4 密钥A 用 Anthropic 协议请求 OpenAI 供应商模型 -> 200（互译 A）
#   5 密钥B 用 OpenAI 协议请求 Anthropic 供应商模型 -> 200（互译 B）
#   6 密钥B 用 Anthropic 协议请求 Anthropic 模型    -> 200
#   7 流式（stream:true）-> SSE
#   8 额度上限 -> 429
set -uo pipefail
GATEWAY="${GATEWAY:?set GATEWAY, e.g. http://<device-ip>:3425}"
mg() { docker exec magpie magpie "$@"; }
N=$(openssl rand -hex 3)
pass=0; fail=0
chk() { # <期望regex> <实际> <描述>
  if printf '%s' "$2" | grep -Eq "$1"; then echo "  ✅ $3 -> $2"; pass=$((pass+1));
  else echo "  ❌ $3 -> $2 (期望 $1)"; fail=$((fail+1)); fi
}

K_A=$(mg gateway-key add "vo-$N" | head -n1 | tr -d '\r')
K_B=$(mg gateway-key add "va-$N" | head -n1 | tr -d '\r')
ID_A=$(mg gateway-key list | awk -v n="vo-$N" '$2==n{print $1}' | head -1)
ID_B=$(mg gateway-key list | awk -v n="va-$N" '$2==n{print $1}' | head -1)
mg gateway-key models "$ID_A" "mockopenai/*" >/dev/null
mg gateway-key models "$ID_B" "mockanth/*"   >/dev/null
mg gateway-key limit  "$ID_A" day --tokens 100 >/dev/null 2>&1
echo "keys: A=$ID_A B=$ID_B (len ${#K_A}/${#K_B})"

oai() { curl -s -o /tmp/v -w '%{http_code}' --max-time 20 -H "Authorization: Bearer $1" \
        -H 'Content-Type: application/json' \
        -d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":8}" \
        "$GATEWAY/v1/chat/completions"; }
anth() { curl -s -o /tmp/v -w '%{http_code}' --max-time 20 -H "Authorization: Bearer $1" \
         -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' \
         -d "{\"model\":\"$2\",\"max_tokens\":8,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" \
         "$GATEWAY/v1/messages"; }

echo
chk '^401$' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$GATEWAY/v1/models")" "1 无密钥"
chk '^200$' "$(oai "$K_A" mockopenai/mockoai-alpha)" "2 A→自己的模型(OpenAI)"
chk '^403$' "$(oai "$K_A" mockanth/mockant-alpha)"   "3 A→别人的模型(应拒)"
chk '^200$' "$(anth "$K_A" mockopenai/mockoai-alpha)" "4 A 用 Anthropic 协议→OpenAI 供应商(互译)"
chk '^200$' "$(oai "$K_B" mockanth/mockant-alpha)"    "5 B 用 OpenAI 协议→Anthropic 供应商(互译)"
chk '^200$' "$(anth "$K_B" mockanth/mockant-alpha)"   "6 B 用 Anthropic 协议→Anthropic 供应商"

# 7 流式
sc=$(curl -s -o /tmp/vs -w '%{http_code}' --max-time 20 -H "Authorization: Bearer $K_A" \
     -H 'Content-Type: application/json' \
     -d '{"model":"mockopenai/mockoai-alpha","messages":[{"role":"user","content":"hi"}],"max_tokens":8,"stream":true}' \
     "$GATEWAY/v1/chat/completions")
chk '^200$' "$sc" "7 流式请求"
if grep -q '^data:' /tmp/vs; then echo "  ✅ 7b 收到 SSE 分块"; pass=$((pass+1));
else echo "  ❌ 7b 未收到 SSE"; fail=$((fail+1)); fi

# 8 额度
last=000; for i in $(seq 1 12); do last=$(oai "$K_A" mockopenai/mockoai-alpha); done
chk '^429$' "$last" "8 超出 token 限额(应 429)"

echo
echo "== 结果：$pass 通过 / $fail 失败 =="
echo "== 上一次 429 的原文 =="; head -c 260 /tmp/v; echo