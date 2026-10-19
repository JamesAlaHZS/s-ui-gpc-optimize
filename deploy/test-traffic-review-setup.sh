#!/bin/bash
# Run only inside a disposable Docker container, never against the host.
set -euo pipefail
test -f /.dockerenv || { echo 'This test requires a disposable container.' >&2; exit 77; }
mkdir -p /run/systemd/system /etc/systemd/system /usr/local/s-ui/deploy /etc/sysctl.d /tmp/mock-bin
cp /src/deploy/*.service /src/deploy/*.timer /usr/local/s-ui/deploy/
printf '#!/bin/sh\nexit 0\n' >/usr/local/s-ui/traffic-review
# Archive permissions may differ; setup must install an executable itself.
for name in sui-monitor panel-user; do
    if command -v useradd >/dev/null; then useradd --system "$name"; else adduser -S -D -H "$name"; fi
done
cat >/tmp/mock-bin/systemctl <<'EOF'
#!/bin/bash
if [[ $1 == show ]]; then printf '%s\n' "${TEST_PANEL_USER:-}"; exit 0; fi
printf '%s\n' "$*" >>/tmp/systemctl-calls
if [[ $1 == is-active ]]; then [[ ${TEST_TIMER_ACTIVE:-0} == 1 ]]; exit; fi
if [[ $1 == is-enabled ]]; then [[ ${TEST_TIMER_ENABLED:-0} == 1 ]]; exit; fi
if [[ ${TEST_COLLECTOR_FAIL:-0} == 1 && $1 == restart ]]; then exit 1; fi
exit 0
EOF
cat >/tmp/mock-bin/ip <<'EOF'
#!/bin/sh
if [ "$1" = '-4' ] || [ "$1" = '-6' ]; then echo 'default via 10.0.0.1 dev eth0'; fi
EOF
for name in conntrack modprobe sysctl; do printf '#!/bin/sh\nexit 0\n' >"/tmp/mock-bin/$name"; done
chmod +x /tmp/mock-bin/*
export PATH="/tmp/mock-bin:$PATH"

bash /src/deploy/setup-traffic-review.sh >/tmp/setup-result
grep -q '账单定时器：未运行；开机自动启用：否' /tmp/setup-result
grep -q '账单同步尚未配置' /tmp/setup-result
grep -q '^User=sui-monitor$' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q -- '--interface eth0 --interval 10s --conntrack' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q '^After=traffic-review-collector.service$' /etc/systemd/system/s-ui.service.d/traffic-review.conf
grep -q 'SUI_TRAFFIC_REVIEW_DB=/var/lib/s-ui-traffic-review/review.db' /etc/systemd/system/s-ui.service.d/traffic-review.conf
grep -q '^restart traffic-review-collector.service$' /tmp/systemctl-calls
! grep -q 'enable.*bill-sync' /tmp/systemctl-calls
test -x /usr/local/bin/traffic-review

touch /etc/s-ui/traffic-review-billing.env
TEST_TIMER_ACTIVE=1 TEST_TIMER_ENABLED=1 bash /src/deploy/setup-traffic-review.sh >/tmp/setup-result
grep -q '账单定时器：运行中；开机自动启用：是' /tmp/setup-result
! grep -q '账单同步尚未配置' /tmp/setup-result
! grep -Eq '(enable|disable|stop).*bill-sync' /tmp/systemctl-calls
TEST_TIMER_ACTIVE=1 bash /src/deploy/setup-traffic-review.sh >/tmp/setup-result
grep -q '账单定时器：运行中；开机自动启用：否' /tmp/setup-result
TEST_TIMER_ENABLED=1 bash /src/deploy/setup-traffic-review.sh >/tmp/setup-result
grep -q '账单定时器：未运行；开机自动启用：是' /tmp/setup-result

TEST_PANEL_USER=panel-user SUI_TRAFFIC_REVIEW_INTERFACE=ens4 bash /src/deploy/setup-traffic-review.sh
grep -q '^User=panel-user$' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q '^User=panel-user$' /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf
bash /src/deploy/setup-traffic-review.sh
test ! -e /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf

if TEST_COLLECTOR_FAIL=1 bash /src/deploy/setup-traffic-review.sh; then echo 'Collector failure was ignored' >&2; exit 1; fi
if SUI_TRAFFIC_REVIEW_INTERFACE='ens4;touch /tmp/injected' bash /src/deploy/setup-traffic-review.sh; then echo 'Invalid interface accepted' >&2; exit 1; fi
test ! -e /tmp/injected
echo 'Installer configuration, repeated setup, shared UID, actual billing timer states and failure checks passed.'
