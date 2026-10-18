#!/bin/bash
set -euo pipefail
[[ -f /.dockerenv ]] || exit 1
mkdir -p /root/.acme.sh /tmp/ssl-domains
export HOME=/root
green='' plain=''
source <(sed -n '/^secure_cert_files()/,/^ssl_cert_issue_main()/p' /src/s-ui.sh | sed '$d')
source <(sed -n '/^cloudflare_token_error_hint()/,/^generate_self_signed_cert()/p' /src/s-ui.sh | sed '$d')
LOGD() { :; }
LOGI() { :; }
LOGE() { echo "$*" >&2; }
confirm() { read -r answer; [[ $answer == y ]]; }
show_menu() { :; }
curl() { touch /tmp/unexpected-diagnostic; return 99; }
export -f curl
cat >/root/.acme.sh/acme.sh <<'SH'
#!/bin/bash
printf '<%s>' "$@" >>/tmp/ssl-domains/args
printf '\n' >>/tmp/ssl-domains/args
[[ $1 != --set-default-ca ]] || exit 0
if [[ ${EXPECT_AUTH:-token} == token ]]; then
  [[ $CF_Token == test-token && -z ${CF_Key:-} && -z ${CF_Email:-} ]] || exit 98
else
  [[ $CF_Key == test-key && $CF_Email == test@example.com && -z ${CF_Token:-} ]] || exit 98
fi
[[ $1 != --issue || ${FAIL_ISSUE:-0} == 0 ]] || exit 1
if [[ $1 == --installcert ]]; then
  while (($#)); do
    case $1 in --fullchain-file|--key-file) printf 'certificate\n' >"$2"; shift ;; esac
    shift
  done
fi
SH
chmod +x /root/.acme.sh/acme.sh
cd /tmp/ssl-domains
touch expanded.example.com
for domain in panel.example.com '*.example.com'; do
  : >args
  printf '1\ny\n%s\n1\ntest-token\n\n' "$domain" | ssl_cert_issue_CF >/dev/null
  [[ $(grep -c '<-d>' args) == 2 ]]
  grep -Fq "<--issue><--dns><dns_cf><-d><$domain><--log>" args
  grep -Fq "<--installcert><-d><$domain><--fullchain-file>" args
done
: >args
printf '2\ny\npanel.example.com\n1\ntest-token\naccount-id\n' | ssl_cert_issue_CF >/dev/null
grep -Fq '<-d><panel.example.com><--force><--log>' args
: >args
printf '1\ny\npanel.example.com\n2\ntest-key\ntest@example.com\n' | EXPECT_AUTH=key ssl_cert_issue_CF >/dev/null
grep -Fq '<-d><panel.example.com><--log>' args
[[ ! -e /tmp/unexpected-diagnostic ]]
curl() {
  [[ "$*" != *test-token* ]] || return 98
  [[ "$*" == *'--config -'* && "$*" == *'zones?per_page=1'* ]] || return 98
  [[ $(cat) == 'header = "Authorization: Bearer test-token"' ]] || return 98
  printf '%s' "${CF_RESPONSE:-}"
}
export -f curl
for response in '{"success":false,"errors":[{"code":6003,"error_chain":[{"code":6111}]}]}' \
                '{"success":false,"errors":[{"code":9109}]}' \
                '{"success":true,"result":[]}'; do
  : >args
  if printf '1\ny\npanel.example.com\n1\ntest-token\n\n' |
      CF_RESPONSE="$response" FAIL_ISSUE=1 ssl_cert_issue_CF >/tmp/cf-error 2>&1; then
    echo 'Failed issuance incorrectly succeeded' >&2; exit 1
  fi
  ! grep -q 'test-token' /tmp/cf-error
  ! grep -q '<--installcert>' args
  case "$response" in
    *6111*) grep -q '6111' /tmp/cf-error ;;
    *9109*) grep -q 'Cloudflare 认证失败' /tmp/cf-error ;;
    *) grep -q 'Zone:Zone:Read' /tmp/cf-error ;;
  esac
done
curl() { touch /tmp/unexpected-diagnostic; return 1; }
CF_Token='' cloudflare_token_error_hint
[[ ! -e /tmp/unexpected-diagnostic ]]
CF_Token=$'test-token\r' cloudflare_token_error_hint >/tmp/cf-error 2>&1
grep -q '换行符' /tmp/cf-error
! grep -q 'test-token' /tmp/cf-error
[[ ! -e /tmp/unexpected-diagnostic ]]
echo 'Passed: exact domain, explicit wildcard without expansion, original force option, Token and Global API Key flows.'
echo 'Passed: failed issuance reports authentication errors without exposing the token or installing a certificate.'
