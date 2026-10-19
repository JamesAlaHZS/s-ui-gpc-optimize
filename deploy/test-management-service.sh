#!/bin/bash
# Only run in a disposable container; all service calls and downloads are mocked.
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run in a disposable Docker container.' >&2; exit 77; }
mkdir -p /run/systemd/system /etc/systemd/system /etc/init.d /tmp/mock-bin
cat >/tmp/mock-bin/systemctl <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >>/tmp/service-calls
case "$1" in
  start|stop|restart|status|is-enabled|enable)
    [[ ${2:-} == s-ui ]] || { touch /tmp/bad-service; exit 64; } ;;
esac
case "$1" in
  start|restart|enable) echo running >/tmp/service-state ;;
  stop) echo dead >/tmp/service-state ;;
  status) printf '   Active: active (%s)\n' "$(cat /tmp/service-state)" ;;
  is-enabled) echo enabled ;;
  daemon-reload) : ;;
  *) exit 64 ;;
esac
EOF
cat >/tmp/mock-bin/rc-service <<'EOF'
#!/bin/bash
[[ ${1:-} == s-ui && $# == 2 ]] || { touch /tmp/bad-service; exit 64; }
printf 'openrc %s\n' "$*" >>/tmp/service-calls
case "$2" in
  start|restart) echo running >/tmp/service-state ;;
  stop) echo dead >/tmp/service-state ;;
  status) [[ $(cat /tmp/service-state) == running ]] ;;
  *) exit 64 ;;
esac
EOF
cat >/tmp/mock-bin/curl <<'EOF'
#!/bin/bash
if [[ ${TEST_INSTALL_FAIL:-0} != 0 ]]; then
    printf '#!/bin/bash\nexit %s\n' "$TEST_INSTALL_FAIL"
    exit 0
fi
cat <<'INSTALLER'
#!/bin/bash
touch /etc/systemd/system/s-ui.service
echo dead >/tmp/service-state
INSTALLER
EOF
cat >/tmp/mock-bin/sleep <<'EOF'
#!/bin/sh
exit 0
EOF
cat >/tmp/mock-bin/install <<'EOF'
#!/bin/bash
[[ ${TEST_COPY_FAIL:-0} != 1 ]] || exit 1
exec /usr/bin/install "$@"
EOF
cat >/tmp/mock-bin/mv <<'EOF'
#!/bin/bash
[[ ${TEST_RENAME_FAIL:-0} != 1 ]] || exit 1
printf '%s\n' "$*" >>/tmp/rename-calls
exec /usr/bin/mv "$@"
EOF
cat >/tmp/mock-bin/cp <<'EOF'
#!/bin/bash
# Catch regression to truncating the management script in place.
[[ ${*: -1} != /usr/bin/s-ui ]] || { touch /tmp/unsafe-copy; exit 1; }
exec /usr/bin/cp "$@"
EOF
chmod +x /tmp/mock-bin/*
export PATH="/tmp/mock-bin:$PATH"

# Exercise the actual bottom-level CLI and interactive menu dispatch.
for mode in cli menu; do
    rm -f /etc/systemd/system/s-ui.service
    echo dead >/tmp/service-state
    if [[ $mode == cli ]]; then
        bash /src/s-ui.sh install >/tmp/management-output
    else
        printf '1\n\n0\n' | bash /src/s-ui.sh >/tmp/management-output
    fi
    [[ $(cat /tmp/service-state) == running ]]
    ! grep -q 'Failed to start' /tmp/management-output
done
rm -f /etc/systemd/system/s-ui.service
echo dead >/tmp/service-state
: >/tmp/service-calls
install_status=0
TEST_INSTALL_FAIL=37 bash /src/s-ui.sh install >/tmp/management-output || install_status=$?
[[ $install_status == 37 ]]
! grep -q '^start ' /tmp/service-calls
[[ $(cat /tmp/service-state) == dead ]]
touch /etc/systemd/system/s-ui.service
for operation in stop start restart; do
    [[ $operation != stop ]] || echo running >/tmp/service-state
    bash /src/s-ui.sh "$operation" >/tmp/management-output
    ! grep -q 'Failed to' /tmp/management-output
    if [[ $operation == stop ]]; then
        [[ $(cat /tmp/service-state) == dead ]]
    else
        [[ $(cat /tmp/service-state) == running ]]
    fi
done

# The numbered menu and CLI must launch the same billing wizard. Completing or
# failing the wizard returns to the menu; CLI arguments and errors stay intact.
mkdir -p /usr/local/s-ui/deploy
cat >/usr/local/s-ui/deploy/configure-billing.sh <<'EOF'
#!/bin/bash
printf 'wizard:%s\n' "$*" >>/tmp/billing-calls
exit "${TEST_BILL_STATUS:-0}"
EOF
for result in 0 37; do
    printf '21\n\n0\n' | TEST_BILL_STATUS=$result bash /src/s-ui.sh >/tmp/management-output
    [[ $(grep -c 'GCP 账单同步配置' /tmp/management-output) == 2 ]]
done
[[ $(grep -c '^wizard:$' /tmp/billing-calls) == 2 ]]
bash /src/s-ui.sh bill --info >/tmp/management-output
grep -q '^wizard:--info$' /tmp/billing-calls
bill_status=0
TEST_BILL_STATUS=37 bash /src/s-ui.sh bill >/tmp/management-output || bill_status=$?
[[ $bill_status == 37 ]]
rm /usr/local/s-ui/deploy/configure-billing.sh
bill_status=0
bash /src/s-ui.sh bill >/tmp/management-output || bill_status=$?
[[ $bill_status == 1 ]]
grep -q '账单配置脚本不存在' /tmp/management-output

# Check service propagation in both init-system implementations, including
# post-stop checks and the restart confirmation helper.
sed '/^if \[\[ \$# > 0 \]\]; then/,$d' /src/s-ui.sh >/tmp/management-functions
(
    set +e
    source /tmp/management-functions >/dev/null
    before_show_menu() { :; }
    confirm() { return 0; }
    touch /etc/init.d/s-ui
    for init_system in systemd openrc; do
        echo running >/tmp/service-state
        stop s-ui 0 >/tmp/management-output
        grep -q 'stopped successfully' /tmp/management-output || exit 1
        confirm_restart s-ui >/tmp/management-output
        grep -q 'Restarted successfully' /tmp/management-output || exit 1
    done
)
test ! -e /tmp/bad-service

# Extract the real installer functions; no installer package manager or network
# is executed. Also run install_s-ui with a tiny synthetic release archive.
sed -n '/^install_management_script() {/,/^echo -e .*executing/p' /src/install.sh | sed '$d' >/tmp/installer-functions
mkdir -p /tmp/release/s-ui
printf '#!/bin/bash\nexit 0\n' >/tmp/release/s-ui/sui
printf '#!/bin/bash\necho installed-management\n' >/tmp/release/s-ui/s-ui.sh
touch /tmp/release/s-ui/s-ui.service
tar czf /tmp/release.tar.gz -C /tmp/release s-ui
(
    source /tmp/installer-functions
    init_system=systemd
    green='' plain=''
    arch() { echo amd64; }
    t() { echo "$1"; }
    fetch() { /usr/bin/cp /tmp/release.tar.gz "$2"; }
    wget() { return 1; }
    verify_checksum() { return 0; }
    config_after_install() { :; }
    prepare_services() { :; }
    install_s-ui fixture >/tmp/installer-output
)
cmp /usr/bin/s-ui /tmp/release/s-ui/s-ui.sh
grep -q '/usr/bin/s-ui$' /tmp/rename-calls
test ! -e /tmp/unsafe-copy

# A later collector setup failure must fail the installer without claiming
# completion or starting the panel. Its already-replaced entry remains intact.
mkdir -p /tmp/release/s-ui/deploy
printf '#!/bin/bash\nexit 37\n' >/tmp/release/s-ui/deploy/setup-traffic-review.sh
tar czf /tmp/release.tar.gz -C /tmp/release s-ui
: >/tmp/service-calls
install_status=0
(
    source /tmp/installer-functions
    init_system=systemd
    green='' plain=''
    arch() { echo amd64; }
    t() { echo "$1"; }
    fetch() { /usr/bin/cp /tmp/release.tar.gz "$2"; }
    wget() { return 1; }
    verify_checksum() { return 0; }
    config_after_install() { :; }
    prepare_services() { :; }
    install_s-ui fixture >/tmp/installer-output
) || install_status=$?
[[ $install_status != 0 ]]
[[ $(cat /tmp/service-state) == dead ]]
! grep -Eq '^(start|enable) ' /tmp/service-calls
! grep -q 'install_finished\|installed-management' /tmp/installer-output
cmp /usr/bin/s-ui /tmp/release/s-ui/s-ui.sh
test -x /usr/bin/s-ui
! compgen -G '/usr/bin/.s-ui.*' >/dev/null

# Replacing a script which is currently being interpreted must preserve the
# old open file descriptor and finish its original tail, beyond Bash's buffer.
sed -n '/^install_management_script() {/,/^install_s-ui()/p' /src/install.sh | sed '$d' >/tmp/atomic-helper
{
    printf '#!/bin/bash\nsource /tmp/atomic-helper\ninstall_management_script /tmp/release/s-ui/s-ui.sh\n'
    for ((i=0; i<3000; i++)); do printf '# old script padding %s\n' "$i"; done
    printf 'echo original-tail >/tmp/original-tail\n'
} >/usr/bin/s-ui
inode_before=$(stat -c '%i' /usr/bin/s-ui)
bash /usr/bin/s-ui
[[ $(cat /tmp/original-tail) == original-tail ]]
[[ $(stat -c '%i' /usr/bin/s-ui) != "$inode_before" ]]
cmp /usr/bin/s-ui /tmp/release/s-ui/s-ui.sh
[[ $(stat -c '%a' /usr/bin/s-ui) == 755 ]]
source /tmp/atomic-helper
for fault in TEST_COPY_FAIL TEST_RENAME_FAIL; do
    printf '#!/bin/bash\necho preserved\n' >/usr/bin/s-ui
    if (export "$fault=1"; install_management_script /tmp/release/s-ui/s-ui.sh); then
        echo 'Atomic replacement failure was ignored' >&2
        exit 1
    fi
    grep -q 'echo preserved' /usr/bin/s-ui
    ! compgen -G '/usr/bin/.s-ui.*' >/dev/null
done
echo 'Passed: menu/CLI install, installer failure propagation, service arguments, collector setup failure, atomic self-replacement and failure preservation.'
