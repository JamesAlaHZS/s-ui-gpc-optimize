#!/bin/bash
# Called by the fork installer after installing the release archive.
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo 'Traffic review setup requires root.' >&2; exit 1; }
[[ -d /run/systemd/system ]] || { echo 'Automatic traffic review setup requires systemd.' >&2; exit 1; }
test -f /usr/local/s-ui/traffic-review
source /etc/os-release

if ! command -v conntrack >/dev/null 2>&1; then
    case "$ID" in
        ubuntu|debian) apt-get update; apt-get install -y conntrack iproute2 ;;
        centos|almalinux|rocky|oracle) yum install -y conntrack-tools iproute ;;
        fedora) dnf install -y conntrack-tools iproute ;;
        arch|manjaro|parch) pacman -S --needed --noconfirm conntrack-tools iproute2 ;;
        opensuse-tumbleweed) zypper -n install conntrack-tools iproute2 ;;
        *) echo "Install conntrack and iproute2 for $ID before continuing." >&2; exit 1 ;;
    esac
fi

iface="${SUI_TRAFFIC_REVIEW_INTERFACE:-$(ip -4 route show default | awk 'NR==1 {print $5}')}"
if [[ -z "$iface" ]]; then
    iface=$(ip -6 route show default | awk 'NR==1 {print $5}')
fi
[[ "$iface" =~ ^[a-zA-Z0-9_-]+$ && "$iface" != lo ]] || { echo 'Cannot detect public interface; set SUI_TRAFFIC_REVIEW_INTERFACE.' >&2; exit 1; }
ip link show dev "$iface" >/dev/null

# A non-root panel and SQLite collector must use the same UID. A root panel
# uses the dedicated monitor user and waits for its Type=notify readiness.
monitor=$(systemctl show s-ui.service -p User --value)
if [[ -z "$monitor" || "$monitor" == root ]]; then
    monitor=sui-monitor
    id "$monitor" >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin "$monitor"
fi
monitor_group=$(id -gn "$monitor")
install -d -m 0700 -o "$monitor" -g "$monitor_group" /var/lib/s-ui-traffic-review
install -d -m 0750 -o root -g "$monitor_group" /etc/s-ui
install -m 0755 /usr/local/s-ui/traffic-review /usr/local/bin/traffic-review
install -m 0644 /usr/local/s-ui/deploy/traffic-review-collector.service /etc/systemd/system/
install -m 0644 /usr/local/s-ui/deploy/traffic-review-bill-sync.service /usr/local/s-ui/deploy/traffic-review-bill-sync.timer /etc/systemd/system/
install -d /etc/systemd/system/traffic-review-collector.service.d /etc/systemd/system/s-ui.service.d
cat >/etc/systemd/system/traffic-review-collector.service.d/installer.conf <<EOF
[Service]
User=$monitor
Group=$monitor_group
ExecStart=
ExecStart=/usr/local/bin/traffic-review collect --db /var/lib/s-ui-traffic-review/review.db --interface $iface --interval 10s --conntrack
EOF
if [[ "$monitor" != sui-monitor ]]; then
    install -d /etc/systemd/system/traffic-review-bill-sync.service.d
    printf '[Service]\nUser=%s\nGroup=%s\n' "$monitor" "$monitor_group" >/etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf
else
    rm -f /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf
fi
cat >/etc/systemd/system/s-ui.service.d/traffic-review.conf <<'EOF'
[Unit]
Wants=traffic-review-collector.service
After=traffic-review-collector.service

[Service]
Environment="SUI_TRAFFIC_REVIEW_DB=/var/lib/s-ui-traffic-review/review.db"
EOF

command -v modprobe >/dev/null 2>&1 && modprobe nf_conntrack || true
if [[ -r /proc/sys/net/netfilter/nf_conntrack_acct ]]; then
    if [[ ! -e /etc/s-ui/nf_conntrack_acct.before-traffic-review ]]; then
        cat /proc/sys/net/netfilter/nf_conntrack_acct >/etc/s-ui/nf_conntrack_acct.before-traffic-review
        chmod 0600 /etc/s-ui/nf_conntrack_acct.before-traffic-review
    fi
    printf 'net.netfilter.nf_conntrack_acct=1\n' >/etc/sysctl.d/90-s-ui-traffic-review.conf
    sysctl -p /etc/sysctl.d/90-s-ui-traffic-review.conf
else
    echo 'Warning: kernel connection byte accounting is unavailable; NIC collection remains enabled.' >&2
fi

systemctl daemon-reload
systemctl enable traffic-review-collector.service
systemctl restart traffic-review-collector.service
echo "Traffic review enabled on $iface. BigQuery billing sync requires your GCP project/dataset configuration; its timer was not enabled."
