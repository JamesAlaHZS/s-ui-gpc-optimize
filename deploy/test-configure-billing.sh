#!/bin/bash
# Destructive filesystem fixtures: run only in a disposable container.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run in a disposable Docker container.' >&2; exit 1; }
mkdir -p /run/systemd/system /usr/local/bin /var/lib/s-ui-traffic-review /test-bin /etc/s-ui
touch /var/lib/s-ui-traffic-review/review.db
export PATH=/test-bin:$PATH
cat >/test-bin/curl <<'SH'
#!/bin/bash
case "${*: -1}" in
  */project/project-id) echo example-vps ;;
  */email) echo vm@example-vps.iam.gserviceaccount.com ;;
  */scopes) echo https://www.googleapis.com/auth/cloud-platform ;;
  *) exit 1 ;;
esac
SH
cat >/test-bin/systemctl <<'SH'
#!/bin/bash
printf '%s\n' "$*" >>/systemctl.log
[[ $1 != show ]] || echo nobody
exit 0
SH
cat >/test-bin/runuser <<'SH'
#!/bin/bash
[[ $1 == -u && $2 == nobody && $3 == -- ]] || exit 99
shift 3
exec "$@"
SH
cat >/usr/local/bin/traffic-review <<'SH'
#!/bin/bash
printf '%s\n' "$*" >>/sync.log
[[ ! -f /fail-query ]] || exit 1
[[ ! -f /fail-import || " $* " == *' --dry-run '* ]] || exit 1
[[ ! -f /bad-result ]] || { echo '{}'; exit 0; }
if [[ -f /empty-result ]]; then
    echo '{"inserted":0,"corrected":0,"unchanged":0}'
elif [[ -f /existing-result ]]; then
    echo '{"inserted":0,"corrected":0,"unchanged":3}'
else
    echo '{"inserted":2,"corrected":1,"unchanged":3}'
fi
SH
chmod +x /test-bin/* /usr/local/bin/traffic-review
printf 'old-config\n' >/etc/s-ui/traffic-review-billing.env
input() { printf '\n\nbilling_export\ngcp_billing_export_v1_ABCDEF_123456\n\n'; }
touch /fail-query
if input | bash /src/deploy/configure-billing.sh >/result 2>&1; then echo 'Accepted failed preflight'; exit 1; fi
[[ $(cat /etc/s-ui/traffic-review-billing.env) == old-config ]]
! grep -q 'enable --now' /systemctl.log
rm /fail-query
touch /fail-import
if input | bash /src/deploy/configure-billing.sh >/result 2>&1; then echo 'Accepted failed import'; exit 1; fi
[[ $(cat /etc/s-ui/traffic-review-billing.env) == old-config ]]
! grep -q 'enable --now' /systemctl.log
rm /fail-import
touch /bad-result
if input | bash /src/deploy/configure-billing.sh >/result 2>&1; then echo 'Accepted invalid sync result'; exit 1; fi
[[ $(cat /etc/s-ui/traffic-review-billing.env) == old-config ]]
! grep -q 'enable --now' /systemctl.log
rm /bad-result
input | bash /src/deploy/configure-billing.sh >/result
grep -q '新增 2 条，修正 1 条，已有且未变 3 条' /result
! grep -q '未返回匹配账单' /result
grep -q '^GCP_QUERY_PROJECT=example-vps$' /etc/s-ui/traffic-review-billing.env
grep -q '^GCP_TARGET_PROJECT=example-vps$' /etc/s-ui/traffic-review-billing.env
[[ $(stat -c %a /etc/s-ui/traffic-review-billing.env) == 640 ]]
[[ $(stat -c %G /etc/s-ui/traffic-review-billing.env) == $(id -gn nobody) ]]
grep -q 'enable --now traffic-review-bill-sync.timer' /systemctl.log
grep -q '每天一次同步' /result
tr -d '\r' </src/deploy/traffic-review-bill-sync.timer >/timer-config
grep -qx 'OnCalendar=\*-\*-\* 00:15:00' /timer-config
[[ $(grep -c '^OnCalendar=' /timer-config) == 1 ]]
grep -qx 'RandomizedDelaySec=15m' /timer-config
grep -qx 'Persistent=true' /timer-config
grep -q -- '--adc --dry-run' /sync.log
touch /empty-result
input | bash /src/deploy/configure-billing.sh >/result
grep -q '新增 0 条，修正 0 条，已有且未变 0 条' /result
grep -q '本次未返回匹配账单' /result
! grep -q '已导入首批数据' /result
rm /empty-result
touch /existing-result
input | bash /src/deploy/configure-billing.sh >/result
grep -q '已有且未变 3 条' /result
! grep -q '未返回匹配账单' /result
rm /existing-result
before=$(wc -l </sync.log)
if printf 'unsafe;touch /injected\n\nbilling_export\ngcp_billing_export_v1_ABCDEF_123456\n\n' | bash /src/deploy/configure-billing.sh >/result 2>&1; then echo 'Accepted unsafe project'; exit 1; fi
[[ ! -f /injected && $(wc -l </sync.log) == "$before" ]]
bash /src/deploy/configure-billing.sh --info >/result
grep -q vm@example-vps /result
echo 'Billing wizard tests passed: metadata, UID, permissions, preflight, import failure, timer and injection.'
