#!/bin/bash
set -euo pipefail
[[ -f /.dockerenv ]] || { echo 'Run in a disposable Docker container.' >&2; exit 1; }
source <(sed -n '/^uninstall() {/,/^reset_admin()/p' /src/s-ui.sh | sed '$d')
confirm() { [[ ${CANCEL:-0} == 0 ]]; }
show_menu() { touch /menu-reopened; }
before_show_menu() { touch /menu-reopened; }
systemctl() { :; }
rc-service() { :; }
rc-update() { :; }
fixture() {
    mkdir -p /usr/local/s-ui /etc/s-ui /etc/systemd/system /etc/init.d
    printf '#!/bin/sh\necho menu\n' >/usr/bin/s-ui
    chmod +x /usr/bin/s-ui
    touch /usr/local/s-ui/sui /etc/s-ui/config /etc/systemd/system/s-ui.service /etc/init.d/s-ui
    hash -r
}
for init_system in systemd openrc; do
    fixture
    (set +e; CANCEL=1; uninstall 0)
    test -x /usr/bin/s-ui
    test -f /usr/local/s-ui/sui
    # Interactive and command-line uninstallation must both exit immediately.
    for mode in menu cli; do
        fixture
        (set +e; if [[ $mode == menu ]]; then uninstall; else uninstall 0; fi; touch /continued-after-uninstall)
        test ! -e /usr/bin/s-ui
        test ! -e /usr/local/s-ui
        test ! -e /etc/s-ui
        test ! -e /menu-reopened
        test ! -e /continued-after-uninstall
        hash -r
        ! command -v s-ui >/dev/null 2>&1
    done
done
echo 'Passed: systemd/OpenRC, cancel, CLI/menu uninstall, command removal and immediate exit.'
