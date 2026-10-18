#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

function LOGD() {
    echo -e "${yellow}[DEG] $* ${plain}"
}

function LOGE() {
    echo -e "${red}[ERR] $* ${plain}"
}

function LOGI() {
    echo -e "${green}[INF] $* ${plain}"
}

[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \n" && exit 1

if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "Failed to check the system OS, please contact the author!" >&2
    exit 1
fi

echo "The OS release is: $release"

# Detect the init system (systemd vs OpenRC used by Alpine)
if [[ "$release" == "alpine" ]]; then
    init_system="openrc"
elif command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
    init_system="systemd"
elif command -v rc-service >/dev/null 2>&1; then
    init_system="openrc"
else
    init_system="systemd"
fi

# Service wrappers so the menu works on both systemd and OpenRC (Alpine).
svc_start()   { if [[ "${init_system}" == "openrc" ]]; then rc-service "$1" start; else systemctl start "$1"; fi; }
svc_stop()    { if [[ "${init_system}" == "openrc" ]]; then rc-service "$1" stop; else systemctl stop "$1"; fi; }
svc_restart() { if [[ "${init_system}" == "openrc" ]]; then rc-service "$1" restart; else systemctl restart "$1"; fi; }
svc_status()  { if [[ "${init_system}" == "openrc" ]]; then rc-service "$1" status; else systemctl status "$1" -l; fi; }
svc_enable()  { if [[ "${init_system}" == "openrc" ]]; then rc-update add "$1" default; else systemctl enable "$1"; fi; }
svc_disable() { if [[ "${init_system}" == "openrc" ]]; then rc-update del "$1" default; else systemctl disable "$1"; fi; }
svc_log()     { if [[ "${init_system}" == "openrc" ]]; then tail -n 200 -f /var/log/s-ui.log; else journalctl -u "$1".service -e --no-pager -f; fi; }

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -p "$1 [Default$2]: " temp
        if [[ x"${temp}" == x"" ]]; then
            temp=$2
        fi
    else
        read -p "$1 [y/n]: " temp
    fi
    if [[ x"${temp}" == x"y" || x"${temp}" == x"Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "Restart the ${1} service" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}Press enter to return to the main menu: ${plain}" && read temp
    show_menu
}

install() {
    bash <(curl -Ls https://raw.githubusercontent.com/JamesAlaHZS/s-ui-gpc-optimize/main/install.sh)
    if [[ $? == 0 ]]; then
        if [[ $# == 0 ]]; then
            start
        else
            start 0
        fi
    fi
}

update() {
    confirm "This function will forcefully reinstall the latest version, and the data will not be lost. Do you want to continue?" "n"
    if [[ $? != 0 ]]; then
        LOGE "Cancelled"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    bash <(curl -Ls https://raw.githubusercontent.com/JamesAlaHZS/s-ui-gpc-optimize/main/install.sh)
    if [[ $? == 0 ]]; then
        LOGI "Update is complete, Panel has automatically restarted "
        exit 0
    fi
}

custom_version() {
    echo "Enter the panel version (like 0.0.1):"
    read panel_version

    if [ -z "$panel_version" ]; then
        echo "Panel version cannot be empty. Exiting."
    exit 1
    fi

    download_link="https://raw.githubusercontent.com/JamesAlaHZS/s-ui-gpc-optimize/main/install.sh"

    install_command="bash <(curl -Ls $download_link) $panel_version"

    echo "Downloading and installing panel version $panel_version..."
    eval $install_command
}

uninstall() {
    confirm "Are you sure you want to uninstall the panel?" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    if [[ "${init_system}" == "openrc" ]]; then
        rc-service s-ui stop
        rc-update del s-ui default
        rm /etc/init.d/s-ui -f
    else
        systemctl disable --now traffic-review-collector.service traffic-review-bill-sync.timer 2>/dev/null || true
        systemctl stop traffic-review-bill-sync.service 2>/dev/null || true
        systemctl stop s-ui
        systemctl disable s-ui
        rm /etc/systemd/system/s-ui.service -f
        rm -f /etc/systemd/system/traffic-review-collector.service /etc/systemd/system/traffic-review-bill-sync.service /etc/systemd/system/traffic-review-bill-sync.timer
        rm -f /etc/systemd/system/s-ui.service.d/traffic-review.conf
        rm -f /etc/systemd/system/traffic-review-collector.service.d/installer.conf /etc/systemd/system/traffic-review-bill-sync.service.d/installer.conf
        systemctl daemon-reload
        systemctl reset-failed
    fi
    rm /etc/s-ui/ -rf
    rm /usr/local/s-ui/ -rf

    echo ""
    echo -e "Uninstalled Successfully, If you want to remove this script, then after exiting the script run ${green}rm /usr/local/s-ui -f${plain} to delete it."
    echo ""

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

reset_admin() {
    echo "It is not recommended to set admin's credentials to default!"
    confirm "Are you sure you want to reset admin's credentials to default ?" "n"
    if [[ $? == 0 ]]; then
        /usr/local/s-ui/sui admin -reset
    fi
    before_show_menu
}

set_admin() {
    echo "It is not recommended to set admin's credentials to a complex text."
    read -p "Please set up your username:" config_account
    read -p "Please set up your password:" config_password
    /usr/local/s-ui/sui admin -username ${config_account} -password ${config_password}
    before_show_menu
}

view_admin() {
    /usr/local/s-ui/sui admin -show
    before_show_menu
}

reset_setting() {
    confirm "Are you sure you want to reset settings to default ?" "n"
    if [[ $? == 0 ]]; then
        /usr/local/s-ui/sui setting -reset
    fi
    before_show_menu
}

set_setting() {
    echo -e "Enter the ${yellow}panel port${plain} (leave blank for existing/default value):"
    read config_port
    echo -e "Enter the ${yellow}panel path${plain} (leave blank for existing/default value):"
    read config_path

    echo -e "Enter the ${yellow}subscription port${plain} (leave blank for existing/default value):"
    read config_subPort
    echo -e "Enter the ${yellow}subscription path${plain} (leave blank for existing/default value):" 
    read config_subPath

    echo -e "${yellow}Initializing, please wait...${plain}"
    params=""
    [ -z "$config_port" ] || params="$params -port $config_port"
    [ -z "$config_path" ] || params="$params -path $config_path"
    [ -z "$config_subPort" ] || params="$params -subPort $config_subPort"
    [ -z "$config_subPath" ] || params="$params -subPath $config_subPath"
    /usr/local/s-ui/sui setting ${params}
    before_show_menu
}

view_setting() {
    /usr/local/s-ui/sui setting -show
    view_uri
    before_show_menu
}

view_uri() {
    info=$(/usr/local/s-ui/sui uri)
    if [[ $? != 0 ]]; then
        LOGE "Get current uri error"
        before_show_menu
    fi
    LOGI "You may access the Panel with following URL(s):"
    echo -e "${green}${info}${plain}"
}

start() {
    check_status $1
    if [[ $? == 0 ]]; then
        echo ""
        LOGI -e "${1} is running, No need to start again, If you need to restart, please select restart"
    else
        svc_start $1
        sleep 2
        check_status $1
        if [[ $? == 0 ]]; then
            LOGI "${1} Started Successfully"
        else
            LOGE "Failed to start ${1}, Probably because it takes longer than two seconds to start, Please check the log information later"
        fi
    fi

    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

stop() {
    check_status $1
    if [[ $? == 1 ]]; then
        echo ""
        LOGI "${1} stopped, No need to stop again!"
    else
        svc_stop $1
        sleep 2
        check_status
        if [[ $? == 1 ]]; then
            LOGI "${1} stopped successfully"
        else
            LOGE "Failed to stop ${1}, Probably because the stop time exceeds two seconds, Please check the log information later"
        fi
    fi

    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

restart() {
    svc_restart $1
    sleep 2
    check_status $1
    if [[ $? == 0 ]]; then
        LOGI "${1} Restarted successfully"
    else
        LOGE "Failed to restart ${1}, Probably because it takes longer than two seconds to start, Please check the log information later"
    fi
    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

status() {
    svc_status s-ui
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    svc_enable $1
    if [[ $? == 0 ]]; then
        LOGI "Set ${1} to boot automatically on startup successfully"
    else
        LOGE "Failed to set ${1} Autostart"
    fi

    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

disable() {
    svc_disable $1
    if [[ $? == 0 ]]; then
        LOGI "Autostart ${1} Cancelled successfully"
    else
        LOGE "Failed to cancel ${1} autostart"
    fi

    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

show_log() {
    svc_log $1
    if [[ $# == 1 ]]; then
        before_show_menu
    fi
}

update_shell() {
    # Certificate verification stays on: this file is about to be installed as
    # /usr/bin/s-ui and run as root.
    wget -O /usr/bin/s-ui -N https://raw.githubusercontent.com/JamesAlaHZS/s-ui-gpc-optimize/main/s-ui.sh
    if [[ $? != 0 ]]; then
        echo ""
        LOGE "Failed to download script, Please check whether the machine can connect Github"
        before_show_menu
    else
        chmod +x /usr/bin/s-ui
        LOGI "Upgrade script succeeded, Please rerun the script" && exit 0
    fi
}

check_status() {
    if [[ "${init_system}" == "openrc" ]]; then
        if [[ ! -f "/etc/init.d/$1" ]]; then
            return 2
        fi
        if rc-service "$1" status >/dev/null 2>&1; then
            return 0
        else
            return 1
        fi
    fi
    if [[ ! -f "/etc/systemd/system/$1.service" ]]; then
        return 2
    fi
    temp=$(systemctl status "$1" | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
    if [[ x"${temp}" == x"running" ]]; then
        return 0
    else
        return 1
    fi
}

check_enabled() {
    if [[ "${init_system}" == "openrc" ]]; then
        if rc-update show default 2>/dev/null | grep -qw "$1"; then
            return 0
        else
            return 1
        fi
    fi
    temp=$(systemctl is-enabled $1)
    if [[ x"${temp}" == x"enabled" ]]; then
        return 0
    else
        return 1
    fi
}

check_uninstall() {
    check_status s-ui
    if [[ $? != 2 ]]; then
        echo ""
        LOGE "Panel is already installed, Please do not reinstall"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

check_install() {
    check_status s-ui
    if [[ $? == 2 ]]; then
        echo ""
        LOGE "Please install the panel first"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

show_status() {
    check_status $1
    case $? in
    0)
        echo -e "${1} state: ${green}Running${plain}"
        show_enable_status $1
        ;;
    1)
        echo -e "${1} state: ${yellow}Not Running${plain}"
        show_enable_status $1
        ;;
    2)
        echo -e "${1} state: ${red}Not Installed${plain}"
        ;;
    esac
}

show_enable_status() {
    check_enabled $1
    if [[ $? == 0 ]]; then
        echo -e "Start ${1} automatically: ${green}Yes${plain}"
    else
        echo -e "Start ${1} automatically: ${red}No${plain}"
    fi
}

check_s-ui_status() {
    count=$(ps -ef | grep "sui" | grep -v "grep" | wc -l)
    if [[ count -ne 0 ]]; then
        return 0
    else
        return 1
    fi
}

show_s-ui_status() {
    check_s-ui_status
    if [[ $? == 0 ]]; then
        echo -e "s-ui state: ${green}Running${plain}"
    else
        echo -e "s-ui state: ${red}Not Running${plain}"
    fi
}

bbr_menu() {
    echo -e "${green}\t1.${plain} Enable BBR"
    echo -e "${green}\t2.${plain} Disable BBR"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -p "Choose an option: " choice
    case "$choice" in
    0)
        show_menu
        ;;
    1)
        enable_bbr
        ;;
    2)
        disable_bbr
        ;;
    *) echo "Invalid choice" ;;
    esac
}

disable_bbr() {
    if ! grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf || ! grep -q "net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf; then
        echo -e "${yellow}BBR is not currently enabled.${plain}"
        exit 0
    fi
    sed -i 's/net.core.default_qdisc=fq/net.core.default_qdisc=pfifo_fast/' /etc/sysctl.conf
    sed -i 's/net.ipv4.tcp_congestion_control=bbr/net.ipv4.tcp_congestion_control=cubic/' /etc/sysctl.conf
    sysctl -p
    if [[ $(sysctl net.ipv4.tcp_congestion_control | awk '{print $3}') == "cubic" ]]; then
        echo -e "${green}BBR has been replaced with CUBIC successfully.${plain}"
    else
        echo -e "${red}Failed to replace BBR with CUBIC. Please check your system configuration.${plain}"
    fi
}

enable_bbr() {
    if grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf && grep -q "net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf; then
        echo -e "${green}BBR is already enabled!${plain}"
        exit 0
    fi
    case "${release}" in
    ubuntu | debian | armbian)
        apt-get update && apt-get install -yqq --no-install-recommends ca-certificates
        ;;
    centos | almalinux | rocky | oracle)
        yum -y update && yum -y install ca-certificates
        ;;
    fedora)
        dnf -y update && dnf -y install ca-certificates
        ;;
    arch | manjaro | parch)
        pacman -Sy --noconfirm ca-certificates
        ;;
    alpine)
        apk update && apk add --no-cache ca-certificates
        ;;
    *)
        echo -e "${red}Unsupported operating system. Please check the script and install the necessary packages manually.${plain}\n"
        exit 1
        ;;
    esac
    echo "net.core.default_qdisc=fq" | tee -a /etc/sysctl.conf
    echo "net.ipv4.tcp_congestion_control=bbr" | tee -a /etc/sysctl.conf
    sysctl -p
    if [[ $(sysctl net.ipv4.tcp_congestion_control | awk '{print $3}') == "bbr" ]]; then
        echo -e "${green}BBR has been enabled successfully.${plain}"
    else
        echo -e "${red}Failed to enable BBR. Please check your system configuration.${plain}"
    fi
}

install_acme() {
    cd ~
    LOGI "install acme..."
    curl https://get.acme.sh | sh
    if [ $? -ne 0 ]; then
        LOGE "install acme failed"
        return 1
    else
        LOGI "install acme succeed"
    fi
    return 0
}

# secure_cert_files makes a certificate directory readable by its owner only.
# These files were chmod 755, which left the private key world readable on a
# multi-user host -- anyone able to read it can impersonate the panel.
secure_cert_files() {
    local dir="$1"
    [ -n "$dir" ] && [ -d "$dir" ] || return 0
    chmod 700 "$dir"
    find "$dir" -type f -name '*.pem' -exec chmod 600 {} +
    find "$dir" -type f ! -name '*.pem' -exec chmod 644 {} +
}

ssl_cert_issue_main() {
    echo -e "${green}\t1.${plain} Get SSL"
    echo -e "${green}\t2.${plain} Revoke"
    echo -e "${green}\t3.${plain} Force Renew"
    echo -e "${green}\t4.${plain} Self-signed Certificate"
    read -p "Choose an option: " choice
    case "$choice" in
        1) ssl_cert_issue ;;
        2) 
            local domain=""
            read -p "Please enter your domain name to revoke the certificate: " domain
            ~/.acme.sh/acme.sh --revoke -d ${domain}
            LOGI "Certificate revoked"
            ;;
        3)
            local domain=""
            read -p "Please enter your domain name to forcefully renew an SSL certificate: " domain
            ~/.acme.sh/acme.sh --renew -d ${domain} --force ;;
        4)
            generate_self_signed_cert
            ;;
        *) echo "Invalid choice" ;;
    esac
}

ssl_cert_issue() {
    if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
        echo "acme.sh could not be found. we will install it"
        install_acme
        if [ $? -ne 0 ]; then
            LOGE "install acme failed, please check logs"
            exit 1
        fi
    fi
    case "${release}" in
    ubuntu | debian | armbian)
        apt update && apt install socat -y
        ;;
    centos | almalinux | rocky | oracle)
        yum -y update && yum -y install socat
        ;;
    fedora)
        dnf -y update && dnf -y install socat
        ;;
    arch | manjaro | parch)
        pacman -Sy --noconfirm socat
        ;;
    alpine)
        apk update && apk add --no-cache socat
        ;;
    *)
        echo -e "${red}Unsupported operating system. Please check the script and install the necessary packages manually.${plain}\n"
        exit 1
        ;;
    esac
    if [ $? -ne 0 ]; then
        LOGE "install socat failed, please check logs"
        exit 1
    else
        LOGI "install socat succeed..."
    fi

    local domain=""
    read -r -p "证书域名（仅普通域名，泛域名请用 Cloudflare DNS 申请）：" domain
    domain=${domain,,}
    if ! ssl_valid_domain "$domain" || [[ "$domain" == \*.* ]]; then
        LOGE '请输入不含协议、端口、路径的普通域名。'
        return 1
    fi
    LOGD "your domain is:${domain},check it..."
    local currentCert=$(~/.acme.sh/acme.sh --list | tail -1 | awk '{print $1}')

    if [[ -n "$currentCert" && "$currentCert" == "$domain" ]]; then
        local certInfo=$(~/.acme.sh/acme.sh --list)
        LOGE "system already has certs here,can not issue again,current certs details:"
        LOGI "$certInfo"
        exit 1
    else
        LOGI "your domain is ready for issuing cert now..."
    fi

    certPath="/root/cert/${domain}"
    mkdir -p "$certPath" || return 1

    local WebPort=80
    read -r -p "please choose which port do you use,default will be 80 port:" WebPort
    # A non-numeric answer used to make the arithmetic comparison an error and
    # then be passed to acme.sh anyway. Anything that is not a port falls back.
    if ! [[ "${WebPort}" =~ ^[0-9]+$ ]] || [ "${WebPort}" -lt 1 ] || [ "${WebPort}" -gt 65535 ]; then
        LOGE "your input ${WebPort} is invalid,will use default port"
        WebPort=80
    fi
    LOGI "will use port:${WebPort} to issue certs,please make sure this port is open..."
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt
    ~/.acme.sh/acme.sh --issue -d "$domain" --standalone --httpport "$WebPort"
    if [ $? -ne 0 ]; then
        LOGE "issue certs failed,please check logs"
        return 1
    else
        LOGE "issue certs succeed,installing certs..."
    fi
    ~/.acme.sh/acme.sh --installcert -d "$domain" \
        --key-file "$certPath/privkey.pem" \
        --fullchain-file "$certPath/fullchain.pem"

    if [ $? -ne 0 ]; then
        LOGE "install certs failed,exit"
        return 1
    else
        LOGI "install certs succeed,enable auto renew..."
    fi

    ~/.acme.sh/acme.sh --upgrade --auto-upgrade
    if [ $? -ne 0 ]; then
        LOGE "auto renew failed, certs details:"
        ls -lah cert/*
        secure_cert_files "$certPath"
        exit 1
    else
        LOGI "auto renew succeed, certs details:"
        ls -lah cert/*
        secure_cert_files "$certPath"
    fi
}

# Validate before using a domain in certificate paths or acme arguments.
ssl_valid_domain() {
    local host="${1#\*.}" label
    [[ ${#host} -le 253 && "$host" == *.* && ! "$host" =~ ^[0-9.]+$ ]] || return 1
    [[ "$1" != *'*'* || "$1" == "*.$host" ]] || return 1
    local -a labels
    IFS=. read -r -a labels <<<"$host"
    for label in "${labels[@]}"; do
        [[ "$label" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || return 1
    done
    [[ "$host" != *. && "$host" != .* && "$host" != *..* ]]
}

ssl_cf_issue() (
    # A subshell keeps credentials out of the menu and subsequent issuances.
    umask 077
    local domain token zone_id zone_name account_id response body status cert_dir
    local -a force=()
    [[ ${1:-} != force ]] || force=(--force)
    read -r -p '证书域名（例如 panel.example.com；只有明确输入 *.example.com 才申请泛域名）：' domain
    domain=${domain,,}
    ssl_valid_domain "$domain" || { LOGE '域名无效：只填完整域名，不含协议、端口、路径或空格。'; return 1; }
    echo "本次仅申请：$domain"
    echo 'API Token：区域 → 区域 → 读取，以及区域 → DNS → 编辑；资源仅选该域名所属站点。'
    read -r -s -p 'Cloudflare API Token：' token
    echo
    [[ "$token" =~ ^[A-Za-z0-9._-]+$ ]] || { LOGE 'Token 为空或格式错误。'; return 1; }
    read -r -p '该域名所属站点的 Zone ID（不是 Account ID）：' zone_id
    [[ "$zone_id" =~ ^[a-fA-F0-9]{32}$ ]] || { LOGE 'Zone ID 应为 32 位十六进制字符，请从站点概述复制。'; return 1; }
    zone_id=${zone_id,,}
    if ! command -v jq >/dev/null 2>&1; then
        case "$release" in
            ubuntu|debian|armbian) apt-get update && apt-get install -y jq ;;
            centos|almalinux|rocky|oracle) yum install -y jq ;;
            fedora) dnf install -y jq ;;
            arch|manjaro|parch) pacman -S --needed --noconfirm jq ;;
            alpine) apk add --no-cache jq ;;
            opensuse-tumbleweed) zypper -n install jq ;;
            *) LOGE '请先安装 jq 后重试。'; return 1 ;;
        esac
        command -v jq >/dev/null 2>&1 || { LOGE 'jq 安装失败。'; return 1; }
    fi
    # Pass the secret on stdin, not in curl's process arguments or logs.
    response=$(printf 'header = "Authorization: Bearer %s"\n' "$token" |
        curl --disable --config - -sS --connect-timeout 10 --max-time 30 \
            --write-out '\n%{http_code}' "https://api.cloudflare.com/client/v4/zones/$zone_id") || { LOGE 'Cloudflare 连接失败，请检查 VPS 网络。'; return 1; }
    status=${response##*$'\n'}
    body=${response%$'\n'*}
    if [[ "$status" != 200 ]] || ! jq -e --arg id "$zone_id" '.success == true and .result.id == $id' <<<"$body" >/dev/null 2>&1; then
        LOGE "Cloudflare 站点校验失败（HTTP $status）：检查 Token 是否有效、是否包含区域读取权限，以及 Zone ID 和站点资源范围是否正确。"
        echo '无需把权限扩大到账号内所有站点；能读取站点也不代表具有 DNS 写入权限。'
        return 1
    fi
    zone_name=$(jq -er '.result.name | strings | ascii_downcase' <<<"$body") || return 1
    ssl_valid_domain "$zone_name" || { LOGE 'Cloudflare 返回的站点名称无效。'; return 1; }
    local host="${domain#\*.}"
    [[ "$host" == "$zone_name" || "$host" == *."$zone_name" ]] || { LOGE '输入的域名不属于这个 Zone ID，请选择正确的站点。'; return 1; }
    account_id=$(jq -r '.result.account.id // empty' <<<"$body") || return 1
    echo "站点校验通过：$zone_name；证书域名仍为：$domain"
    [[ -x "$HOME/.acme.sh/acme.sh" ]] || install_acme || return 1
    # Explicit token + Zone ID overrides inherited/saved account-wide zone hints.
    unset CF_Key CF_Email CF_Account_ID CF_Zone_ID CF_Token
    export CF_Token="$token" CF_Zone_ID="$zone_id"
    [[ ! "$account_id" =~ ^[a-fA-F0-9]{32}$ ]] || export CF_Account_ID="$account_id"
    "$HOME/.acme.sh/acme.sh" --issue --dns dns_cf --server letsencrypt \
        --keylength ec-256 -d "$domain" "${force[@]}" || { LOGE '证书申请失败。若出现 invalid domain，检查 Zone ID、Token 范围和区域读取权限；TXT 写入失败检查 DNS 编辑权限。已有证书未删除。'; return 1; }
    cert_dir="/root/cert-CF/$domain"
    mkdir -p "$cert_dir" || return 1
    chmod 700 /root/cert-CF "$cert_dir" || return 1
    "$HOME/.acme.sh/acme.sh" --install-cert -d "$domain" --ecc \
        --fullchain-file "$cert_dir/fullchain.pem" --key-file "$cert_dir/privkey.pem" || { LOGE '证书安装失败，请检查 acme.sh 输出。'; return 1; }
    secure_cert_files "$cert_dir"
    "$HOME/.acme.sh/acme.sh" --upgrade --auto-upgrade || LOGE '证书已安装，但 acme.sh 自动升级设置失败，请检查定时续期。'
    echo "证书已安装：$cert_dir/fullchain.pem"
    echo "私钥路径：$cert_dir/privkey.pem"
    echo '在面板证书设置中填写这两个路径，再应用更改。泛域名不自动包含其根域名。'
)

ssl_cert_issue_CF() {
    echo '1) 申请 Cloudflare DNS 证书（仅输入的域名）'
    echo '2) 强制重新申请（修正旧证书或域名范围）'
    echo '3) 返回菜单'
    local choice
    read -r -p '请选择 [1-3]：' choice
    case "$choice" in
        1) ssl_cf_issue || return 1 ;;
        2) ssl_cf_issue force || return 1 ;;
        3) ;;
        *) LOGE '选项无效。'; return 1 ;;
    esac
    show_menu
}

generate_self_signed_cert() {
    cert_dir="/etc/sing-box"
    mkdir -p "$cert_dir"
    LOGI "Choose certificate type:"
    echo -e "${green}\t1.${plain} Ed25519 (*recommended*)"
    echo -e "${green}\t2.${plain} RSA 2048"
    echo -e "${green}\t3.${plain} RSA 4096"
    echo -e "${green}\t4.${plain} ECDSA prime256v1"
    echo -e "${green}\t5.${plain} ECDSA secp384r1"
    read -p "Enter your choice [1-5, default 1]: " cert_type
    cert_type=${cert_type:-1}

    case "$cert_type" in
        1)
            algo="ed25519"
            key_opt="-newkey ed25519"
            ;;
        2)
            algo="rsa"
            key_opt="-newkey rsa:2048"
            ;;
        3)
            algo="rsa"
            key_opt="-newkey rsa:4096"
            ;;
        4)
            algo="ecdsa"
            key_opt="-newkey ec -pkeyopt ec_paramgen_curve:prime256v1"
            ;;
        5)
            algo="ecdsa"
            key_opt="-newkey ec -pkeyopt ec_paramgen_curve:secp384r1"
            ;;
        *)
            algo="ed25519"
            key_opt="-newkey ed25519"
            ;;
    esac

    LOGI "Generating self-signed certificate ($algo)..."
    sudo openssl req -x509 -nodes -days 3650 $key_opt \
        -keyout "${cert_dir}/self.key" \
        -out "${cert_dir}/self.crt" \
        -subj "/CN=myserver"
    if [[ $? -eq 0 ]]; then
        sudo chmod 600 "${cert_dir}/self."*
        LOGI "Self-signed certificate generated successfully!"
        LOGI "Certificate path: ${cert_dir}/self.crt"
        LOGI "Key path: ${cert_dir}/self.key"
    else
        LOGE "Failed to generate self-signed certificate."
    fi
    before_show_menu
}

show_usage() {
    echo -e "S-UI Control Menu Usage"
    echo -e "------------------------------------------"
    echo -e "SUBCOMMANDS:" 
    echo -e "s-ui              - Admin Management Script"
    echo -e "s-ui start        - Start s-ui"
    echo -e "s-ui stop         - Stop s-ui"
    echo -e "s-ui restart      - Restart s-ui"
    echo -e "s-ui status       - Current Status of s-ui"
    echo -e "s-ui enable       - Enable Autostart on OS Startup"
    echo -e "s-ui disable      - Disable Autostart on OS Startup"
    echo -e "s-ui log          - Check s-ui Logs"
    echo -e "s-ui update       - Update"
    echo -e "s-ui bill         - 配置 GCP 账单同步"
    echo -e "s-ui bill --info  - 查看 VPS 项目与服务账号"
    echo -e "s-ui install      - Install"
    echo -e "s-ui uninstall    - Uninstall"
    echo -e "s-ui help         - Control Menu Usage"
    echo -e "------------------------------------------"
}

show_menu() {
  echo -e "
  ${green}S-UI Admin Management Script ${plain}
————————————————————————————————
  ${green}0.${plain} Exit
————————————————————————————————
  ${green}1.${plain} Install
  ${green}2.${plain} Update
  ${green}3.${plain} Custom Version
  ${green}4.${plain} Uninstall
————————————————————————————————
  ${green}5.${plain} Reset admin credentials to default
  ${green}6.${plain} Set admin credentials
  ${green}7.${plain} View admin credentials
————————————————————————————————
  ${green}8.${plain} Reset Panel Settings
  ${green}9.${plain} Set Panel settings
  ${green}10.${plain} View Panel Settings
————————————————————————————————
  ${green}11.${plain} S-UI Start
  ${green}12.${plain} S-UI Stop
  ${green}13.${plain} S-UI Restart
  ${green}14.${plain} S-UI Check State
  ${green}15.${plain} S-UI Check Logs
  ${green}16.${plain} S-UI Enable Autostart
  ${green}17.${plain} S-UI Disable Autostart
————————————————————————————————
  ${green}18.${plain} Enable or Disable BBR
  ${green}19.${plain} SSL Certificate Management
  ${green}20.${plain} Cloudflare SSL Certificate
————————————————————————————————
 "
    show_status s-ui
    echo && read -p "Please enter your selection [0-20]: " num

    case "${num}" in
    0)
        exit 0
        ;;
    1)
        check_uninstall && install
        ;;
    2)
        check_install && update
        ;;
    3)
        check_install && custom_version
        ;;
    4)
        check_install && uninstall
        ;;
    5)
        check_install && reset_admin
        ;;
    6)
        check_install && set_admin
        ;;
    7)
        check_install && view_admin
        ;;
    8)
        check_install && reset_setting
        ;;
    9)
        check_install && set_setting
        ;;
    10)
        check_install && view_setting
        ;;
    11)
        check_install && start s-ui
        ;;
    12)
        check_install && stop s-ui
        ;;
    13)
        check_install && restart s-ui
        ;;
    14)
        check_install && status s-ui
        ;;
    15)
        check_install && show_log s-ui
        ;;
    16)
        check_install && enable s-ui
        ;;
    17)
        check_install && disable s-ui
        ;;
    18)
        bbr_menu
        ;;
    19)
        ssl_cert_issue_main
        ;;
    20)
        ssl_cert_issue_CF
        ;;
    *)
        LOGE "Please enter the correct number [0-20]"
        ;;
    esac
}

if [[ $# > 0 ]]; then
    case $1 in
    "start")
        check_install 0 && start s-ui 0
        ;;
    "stop")
        check_install 0 && stop s-ui 0
        ;;
    "restart")
        check_install 0 && restart s-ui 0
        ;;
    "status")
        check_install 0 && status 0
        ;;
    "enable")
        check_install 0 && enable s-ui 0
        ;;
    "disable")
        check_install 0 && disable s-ui 0
        ;;
    "log")
        check_install 0 && show_log s-ui 0
        ;;
    "update")
        check_install 0 && update 0
        ;;
    "install")
        check_uninstall 0 && install 0
        ;;
    "bill")
        shift
        bash /usr/local/s-ui/deploy/configure-billing.sh "$@"
        ;;
    "uninstall")
        check_install 0 && uninstall 0
        ;;
    *) show_usage ;;
    esac
else
    show_menu
fi
