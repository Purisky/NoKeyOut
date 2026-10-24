#!/usr/bin/env bash
# nokeyout-cert —— 管理面自签 TLS 证书的签发 / 续签 / 重签
#
#   sudo ./nokeyout-cert.sh
#   sudo ./nokeyout-cert.sh --ip <device-ip> --dns nokeyout.local
#   sudo ./nokeyout-cert.sh --reissue
#   sudo ./nokeyout-cert.sh --print
#
set -euo pipefail
umask 077

TLS_DIR=/var/lib/nokeyout/tls
LEAF_DAYS=397
EXPIRE_WARN=2592000
IP_LIST=(); DNS_LIST=()
REISSUE=0; PRINT_ONLY=0; QUIET=0; NO_RESTART="${NK_CERT_NO_RESTART:-0}"

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; }
say() { [ "$QUIET" = 1 ] || printf '%s\n' "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do case "$1" in
  --ip) IP_LIST+=("$2"); shift 2;;
  --dns) DNS_LIST+=("$2"); shift 2;;
  --tls-dir) TLS_DIR="$2"; shift 2;;
  --days) LEAF_DAYS="$2"; shift 2;;
  --reissue) REISSUE=1; shift;;
  --print) PRINT_ONLY=1; shift;;
  --quiet) QUIET=1; shift;;
  --no-restart) NO_RESTART=1; shift;;
  -h|--help) usage; exit 0;;
  *) die "未知参数 $1（--help 看用法）";;
esac; done

command -v openssl >/dev/null 2>&1 || die "需要 openssl"
[[ "$LEAF_DAYS" =~ ^[0-9]+$ ]] && [ "$LEAF_DAYS" -gt 0 ] || die "--days 必须是正整数"

CRT="$TLS_DIR/cert.pem"
KEY="$TLS_DIR/key.pem"

SANS=""
add_san() {
  local v="$1"
  case ",$SANS," in *",$v,"*) return 0;; esac
  SANS="${SANS:+$SANS,}$v"
}
is_ipv4() { [[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]; }
is_ipv6() { [[ "$1" =~ ^[0-9a-fA-F:]+$ ]] && [[ "$1" == *:* ]]; }
for ip in ${IP_LIST[@]+"${IP_LIST[@]}"}; do is_ipv4 "$ip" || is_ipv6 "$ip" || die "不是合法 IP：$ip"; done
for d in ${DNS_LIST[@]+"${DNS_LIST[@]}"}; do [[ "$d" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]] || die "不是合法 DNS 名：$d"; done

auto_ips() {
  local out=""
  command -v hostname >/dev/null 2>&1 && out="$(hostname -I 2>/dev/null || true)"
  if [ -z "$(printf '%s' "$out" | tr -d ' \t')" ] && command -v ip >/dev/null 2>&1; then
    out="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 || true)"
  fi
  printf '%s\n' $out 2>/dev/null || true
}
PRIMARY="${IP_LIST[0]:-}"
if [ -z "$PRIMARY" ] && [ "$PRINT_ONLY" != 1 ]; then
  while read -r ip; do [ -n "$ip" ] || continue; PRIMARY="$ip"; break; done < <(auto_ips)
fi

add_san "IP:127.0.0.1"
add_san "IP:::1"
add_san "DNS:localhost"
HOSTN="$(hostname -s 2>/dev/null || true)"
[ -n "$HOSTN" ] && add_san "DNS:$HOSTN"
for d in ${DNS_LIST[@]+"${DNS_LIST[@]}"}; do add_san "DNS:$d"; done
[ -n "$PRIMARY" ] && add_san "IP:$PRIMARY"
for ip in ${IP_LIST[@]+"${IP_LIST[@]}"}; do add_san "IP:$ip"; done
while read -r ip; do [ -n "$ip" ] || continue; add_san "IP:$ip"; done < <(auto_ips)

fp_of() { [ -s "$1" ] && openssl x509 -in "$1" -noout -fingerprint -sha256 2>/dev/null | sed 's/.*=//' || true; }
pub_of_crt() { openssl x509 -in "$1" -noout -pubkey 2>/dev/null; }
pub_of_key() { openssl pkey -in "$1" -pubout 2>/dev/null; }

show() {
  [ -s "$CRT" ] || { say "服务端证书：无（$CRT）"; return 0; }
  say "服务端证书：$CRT"
  openssl x509 -in "$CRT" -noout -subject -issuer -dates -ext subjectAltName 2>/dev/null | sed 's/^/  /'
  say "  指纹(SHA-256)：$(fp_of "$CRT")"
}

if [ "$PRINT_ONLY" = 1 ]; then show; exit 0; fi
[ -n "$PRIMARY" ] || die "没给 --ip 也自动探测不到 IPv4；请显式 --ip <设备IP>"

NEED=0; WHY=()
if [ ! -s "$CRT" ] || [ ! -s "$KEY" ]; then NEED=1; WHY+=("尚无证书")
elif [ "$REISSUE" = 1 ]; then NEED=1; WHY+=("显式 --reissue")
else
  openssl x509 -checkend "$EXPIRE_WARN" -noout -in "$CRT" >/dev/null 2>&1 || { NEED=1; WHY+=("30 天内过期"); }
  [ "$(pub_of_crt "$CRT")" = "$(pub_of_key "$KEY")" ] || { NEED=1; WHY+=("证书与私钥不匹配"); }
  openssl x509 -in "$CRT" -noout -checkip "$PRIMARY" >/dev/null 2>&1 || { NEED=1; WHY+=("SAN 未覆盖 $PRIMARY"); }
fi
if [ "$NEED" = 0 ]; then say "证书已就绪，无需改动"; show; exit 0; fi
say "重签原因：$(IFS='、'; echo "${WHY[*]}")"
say "SAN：$SANS"

install -d -m 0755 "$TLS_DIR"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$KEY" -out "$CRT" \
  -days "$LEAF_DAYS" -sha256 -subj "/CN=nokeyout" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
  -addext "extendedKeyUsage=serverAuth" \
  -addext "subjectKeyIdentifier=hash" \
  -addext "subjectAltName=$SANS"
chmod 600 "$KEY"; chmod 644 "$CRT"
if [ "$(id -u)" = 0 ] && id nokeyout >/dev/null 2>&1; then
  chown nokeyout:nokeyout "$CRT" "$KEY"
  chmod 600 "$KEY"; chmod 644 "$CRT"
fi
show

PANEL_PORT="$(sed -n 's/^PANEL_PORT=//p' /etc/nokeyout/panel.env 2>/dev/null | head -1 || true)"
PANEL_PORT="${PANEL_PORT:-8443}"
if [ "$NO_RESTART" = 1 ] || [ "$TLS_DIR" != /var/lib/nokeyout/tls ]; then
  say "未重启管理面：证书在下一次启动时生效"
elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet nokeyout-panel 2>/dev/null; then
  say "重启 nokeyout-panel 以加载新证书…"
  systemctl try-restart nokeyout-panel
  for i in $(seq 1 20); do
    sleep 0.5
    if curl -fsSk "https://127.0.0.1:$PANEL_PORT/healthz" >/dev/null 2>&1; then
      say "✅ 管理面已起来（自签 TLS）"; break
    fi
    [ "$i" = 20 ] && say "⚠️ 20 次探活未通过：systemctl status nokeyout-panel"
  done
fi
say ""
say "浏览器首次访问 https://$PRIMARY:$PANEL_PORT/ 会提示证书不受信任。"
