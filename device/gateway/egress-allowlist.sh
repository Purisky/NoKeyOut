#!/usr/bin/env bash
# egress-allowlist.sh — 网关默认拒绝所有出网，仅放行白名单域名解析出的 IP。
# 用法(在 Orange Pi 上, root):
#   sudo ./egress-allowlist.sh api.openai.com api.anthropic.com generativelanguage.googleapis.com relay.example.com
#
# 依赖: nftables, getent(glibc) 或 dig。建议加 systemd timer 定期刷新 IP。
# 说明: 这是"起步版"——基于域名解析快照。CDN 会轮换 IP，生产上建议改用
#       Squid/透明代理做域名级白名单，或供应商提供的固定出口段。
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "需要 root"; exit 1; }
command -v nft >/dev/null || { echo "缺少 nftables"; exit 1; }

DOMAINS=("$@")
[ ${#DOMAINS[@]} -gt 0 ] || { echo "用法: $0 <域名> [域名...]"; exit 1; }

TABLE=egress
SET=allow4
SET6=allow6

nft list table inet "$TABLE" >/dev/null 2>&1 || nft add table inet "$TABLE"
nft list set inet "$TABLE" "$SET"  >/dev/null 2>&1 || nft add set inet "$TABLE" "$SET"  '{ type ipv4_addr; flags interval; }'
nft list set inet "$TABLE" "$SET6" >/dev/null 2>&1 || nft add set inet "$TABLE" "$SET6" '{ type ipv6_addr; flags interval; }'

# 刷新集合
nft flush set inet "$TABLE" "$SET"  || true
nft flush set inet "$TABLE" "$SET6" || true

add_ips() {
  local d="$1" ip
  while read -r ip; do
    [ -n "$ip" ] || continue
    if [[ "$ip" == *:* ]]; then
      nft add element inet "$TABLE" "$SET6" "{ $ip }" 2>/dev/null || true
    else
      nft add element inet "$TABLE" "$SET"  "{ $ip }" 2>/dev/null || true
    fi
  done < <(getent ahosts "$d" 2>/dev/null | awk '{print $1}' | sort -u)
}

for d in "${DOMAINS[@]}"; do
  echo "解析 $d ..."
  add_ips "$d"
done

# 规则: 默认丢，放行 lo / established / DNS / allowlist
nft flush chain inet "$TABLE" output 2>/dev/null || true
nft add chain inet "$TABLE" output '{ type filter hook output priority 0; policy drop; }'

nft add rule inet "$TABLE" output oifname "lo" accept
nft add rule inet "$TABLE" output ct state established,related accept
nft add rule inet "$TABLE" output udp dport 53 accept
nft add rule inet "$TABLE" output tcp dport 53 accept
nft add rule inet "$TABLE" output ip  daddr @"$SET"  accept
nft add rule inet "$TABLE" output ip6 daddr @"$SET6" accept

echo "已应用出网白名单。当前集合:"
nft list set inet "$TABLE" "$SET"
