#!/bin/bash
# Run only inside a disposable Docker container, never against the host.
set -euo pipefail
test -f /.dockerenv || { echo 'This test requires a disposable container.' >&2; exit 77; }
mkdir -p /run/systemd/system /etc/systemd/system /usr/local/s-ui/deploy /etc/sysctl.d /tmp/mock-bin
cp /src/deploy/*.service /src/deploy/*.timer /usr/local/s-ui/deploy/
printf '#!/bin/sh\nexit 0\n' >/usr/local/s-ui/traffic-review
chmod +x /usr/local/s-ui/traffic-review
for name in sui-monitor panel-user; do
    if command -v useradd >/dev/null; then useradd --system "$name"; else adduser -S -D -H "$name"; fi
done
cat >/tmp/mock-bin/systemctl <<'EOF'
#!/bin/bash
if [[ $1 == show ]]; then printf '%s\n' "${TEST_PANEL_USER:-}"; exit 0; fi
printf '%s\n' "$*" >>/tmp/systemctl-calls
if [[ ${TEST_COLLECTOR_FAIL:-0} == 1 && $1 == restart ]]; then exit 1; fi
EOF
cat >/tmp/mock-bin/ip <<'EOF'
#!/bin/sh
if [ "$1" = '-4' ] || [ "$1" = '-6' ]; then echo 'default via 10.0.0.1 dev eth0'; fi
EOF
for name in conntrack modprobe sysctl; do printf '#!/bin/sh\nexit 0\n' >"/tmp/mock-bin/$name"; done
chmod +x /tmp/mock-bin/*
export PATH="/tmp/mock-bin:$PATH"

bash /src/deploy/setup-traffic-review.sh
grep -q '^User=sui-monitor$' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q -- '--interface eth0 --interval 10s --conntrack' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q '^After=traffic-review-collector.service$' /etc/systemd/system/s-ui.service.d/traffic-review.conf
grep -q 'SUI_TRAFFIC_REVIEW_DB=/var/lib/s-ui-traffic-review/review.db' /etc/systemd/system/s-ui.service.d/traffic-review.conf
grep -q '^restart traffic-review-collector.service$' /tmp/systemctl-calls
! grep -q 'enable.*bill-sync' /tmp/systemctl-calls
test -x /usr/local/bin/traffic-review

TEST_PANEL_USER=panel-user SUI_TRAFFIC_REVIEW_INTERFACE=ens4 bash /src/deploy/setup-traffic-review.sh
grep -q '^User=panel-user$' /etc/systemd/system/traffic-review-collector.service.d/installer.conf
grep -q '^User=panel-user$' /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf
bash /src/deploy/setup-traffic-review.sh
test ! -e /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf

if TEST_COLLECTOR_FAIL=1 bash /src/deploy/setup-traffic-review.sh; then echo 'Collector failure was ignored' >&2; exit 1; fi
if SUI_TRAFFIC_REVIEW_INTERFACE='ens4;touch /tmp/injected' bash /src/deploy/setup-traffic-review.sh; then echo 'Invalid interface accepted' >&2; exit 1; fi
test ! -e /tmp/injected
echo 'Installer configuration, repeated setup, shared UID, disabled billing timer and failure checks passed.'
