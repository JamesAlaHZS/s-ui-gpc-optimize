#!/bin/bash
# Isolate certificate paths and ACME/Cloudflare calls in a disposable container.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run in a disposable Docker container.' >&2; exit 1; }
command -v jq >/dev/null
mkdir -p /test-bin /root/.acme.sh /root/cert-CF/keep.example.org
printf 'keep certificate\n' >/root/cert-CF/keep.example.org/fullchain.pem
printf 'keep account\n' >/root/.acme.sh/account.conf
# Load only the functions under test, never the management menu or OS setup.
source <(sed -n '/^secure_cert_files()/,/^ssl_cert_issue_main()/p' /src/s-ui.sh | sed '$d')
source <(sed -n '/^ssl_valid_domain()/,/^generate_self_signed_cert()/p' /src/s-ui.sh | sed '$d')
source <(sed -n '/^ssl_cert_issue() {/,/^ssl_valid_domain()/p' /src/s-ui.sh | sed '$d')
LOGE() { echo "$*" >&2; }
LOGD() { :; }
LOGI() { :; }
release=ubuntu
export PATH=/test-bin:$PATH
export HOME=/root
export CF_Token=stale CF_Zone_ID=ffffffffffffffffffffffffffffffff CF_Account_ID=stale CF_Key=stale CF_Email=stale
printf '#!/bin/sh\nexit 0\n' >/test-bin/apt
chmod +x /test-bin/apt
cat >/test-bin/curl <<'SH'
#!/bin/bash
printf '%s\n' "$@" >/curl.args
cat >/curl.header
case "${SSL_TEST_MODE:-ok}" in
  denied) printf '{"success":false,"errors":[{"code":9109}]}\n403'; exit ;;
  invalid_json) printf 'bad-json\n200'; exit ;;
  wrong_zone) name=other.example.org ;;
  *) name=example.com ;;
esac
printf '{"success":true,"result":{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"%s","account":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}\n200' "$name"
SH
cat >/root/.acme.sh/acme.sh <<'SH'
#!/bin/bash
printf '<%s>' "$@" >>/acme.args
printf '\n' >>/acme.args
if [[ ${SSL_TEST_MODE:-ok} != http_fail ]]; then
  [[ $CF_Token == test-token && $CF_Zone_ID == aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa && $CF_Account_ID == bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb && -z ${CF_Key:-} && -z ${CF_Email:-} ]] || exit 98
fi
[[ ${SSL_TEST_MODE:-ok} != *_fail || $1 != --issue ]] || exit 1
if [[ $1 == --install-cert ]]; then
  while (($#)); do
    case "$1" in --fullchain-file|--key-file) printf 'new certificate\n' >"$2"; shift ;; esac
    shift
  done
fi
SH
chmod +x /test-bin/curl /root/.acme.sh/acme.sh
input() { printf '%s\ntest-token\naaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' "$1"; }
# A matching file must never cause shell expansion of an explicit wildcard.
cd /test-bin
touch child.example.com
input panel.example.com | ssl_cf_issue >/result
grep -q '<-d><panel.example.com>' /acme.args
! grep -q '\*\.panel.example.com' /acme.args
[[ $(grep -c '<-d>' /acme.args) == 2 ]]
[[ $(stat -c %a /root/cert-CF/panel.example.com/privkey.pem) == 600 ]]
! grep -q test-token /curl.args
! grep -q test-token /result
grep -q 'Authorization: Bearer test-token' /curl.header
[[ $CF_Token == stale && $CF_Zone_ID == ffffffffffffffffffffffffffffffff ]]
[[ $(cat /root/.acme.sh/account.conf) == 'keep account' ]]
[[ $(cat /root/cert-CF/keep.example.org/fullchain.pem) == 'keep certificate' ]]
: >/acme.args
input '*.example.com' | ssl_cf_issue force >/result
grep -q '<-d><\*.example.com><--force>' /acme.args
grep -q '<--install-cert><-d><\*.example.com><--ecc>' /acme.args
[[ $(grep -c '<-d>' /acme.args) == 2 ]]
# Failed issuance must retain this domain's already installed certificate too.
printf 'previous certificate\n' >/root/cert-CF/panel.example.com/fullchain.pem
if input panel.example.com | SSL_TEST_MODE=issue_fail ssl_cf_issue >/result 2>&1; then exit 1; fi
[[ $(cat /root/cert-CF/panel.example.com/fullchain.pem) == 'previous certificate' ]]
for mode in denied invalid_json wrong_zone; do
  : >/acme.args
  if input panel.example.com | SSL_TEST_MODE=$mode ssl_cf_issue >/result 2>&1; then echo "Accepted $mode"; exit 1; fi
  [[ ! -s /acme.args ]]
done
for domain in '' '../bad' 'https://panel.example.com' 'panel.example.com:443' 'a..example.com' 'x;touch /injected' '*.*.example.com' '-bad.example.com'; do
  if ssl_valid_domain "$domain"; then echo "Accepted $domain"; exit 1; fi
done
mkdir -p /root/cert/http.example.com
printf 'previous HTTP certificate\n' >/root/cert/http.example.com/fullchain.pem
if printf 'http.example.com\n80\n' | SSL_TEST_MODE=http_fail ssl_cert_issue >/result 2>&1; then exit 1; fi
[[ $(cat /root/cert/http.example.com/fullchain.pem) == 'previous HTTP certificate' ]]
[[ $(cat /root/.acme.sh/account.conf) == 'keep account' ]]
echo 'SSL tests passed: exact domain, explicit wildcard, force, permissions, credential isolation, API failures and certificate preservation.'
