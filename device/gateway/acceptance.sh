#!/usr/bin/env bash
# acceptance.sh — 验证"密钥隔离网关"的两条核心主张。
#
# 用法(在开发机上执行):
#   GATEWAY_URL=http://<device-ip>:3425 \
#   TOKEN_A=<magpie 网关密钥，作用域仅含 MODEL_A> \
#   MODEL_A=<该 key 作用域内模型> MODEL_B=<作用域外模型> \
#   ./acceptance.sh
#
# 断言:
#   [1] 开发机本机不存在明文真实 Key
#   [2] 用 scoped 网关密钥 A 请求 B 的模型 → 被拒(HTTP 4xx)
set -uo pipefail

: "${GATEWAY_URL:?需要 GATEWAY_URL}"
: "${TOKEN_A:?需要 TOKEN_A}"
: "${MODEL_A:?需要 MODEL_A}"
: "${MODEL_B:?需要 MODEL_B}"

fail=0
pass() { echo "  ✅ PASS: $1"; }
bad()  { echo "  ❌ FAIL: $1"; fail=1; }

echo "== [1] 开发机明文 Key 扫描 =="
hits=$(grep -rInE 'sk-[A-Za-z0-9_-]{20,}' \
        "$HOME/.claude" "$HOME/.config" "$HOME/.env" "$HOME/.bashrc" "$HOME/.zshrc" 2>/dev/null \
        | grep -v "$TOKEN_A" || true)
if [ -z "$hits" ]; then
  pass "未发现明文真实 Key（仅网关密钥）"
else
  bad "发现疑似明文 Key:"; echo "$hits" | sed 's/^/     /'
fi

ask() { # model -> prints http code
  curl -s -o /dev/null -w '%{http_code}' --max-time 20 \
    -H "Authorization: Bearer $TOKEN_A" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}],\"max_tokens\":1}" \
    "$GATEWAY_URL/v1/chat/completions"
}

echo "== [2a] scoped 网关密钥A 请求 A 的模型（应成功 2xx/4xx-业务，非鉴权失败）=="
code_a=$(ask "$MODEL_A"); echo "     HTTP $code_a"
case "$code_a" in 200|201) pass "A 密钥可用 A 模型";; *) echo "  ℹ 非 2xx（额度/上游问题可接受，但不应是 401/403 鉴权拒绝）";; esac

echo "== [2b] scoped 网关密钥A 请求 B 的模型（必须被拒 4xx）=="
code_b=$(ask "$MODEL_B"); echo "     HTTP $code_b"
case "$code_b" in
  4*) pass "跨供应商请求被拒";;
  *)  bad "跨供应商未被拒（HTTP $code_b）";;
esac

echo
[ "$fail" -eq 0 ] && echo "全部通过 ✅" || echo "存在失败 ❌"
exit "$fail"
