#!/bin/bash
# Configure VM metadata authentication; never downloads or stores account keys.
set -euo pipefail
fail() { printf '错误：%s\n' "$*" >&2; exit 1; }
metadata() { curl --noproxy '*' -fsS --connect-timeout 2 --max-time 5 -H 'Metadata-Flavor: Google' "http://metadata.google.internal/computeMetadata/v1/$1" 2>/dev/null || true; }
vm_project=$(metadata project/project-id)
if [[ ${1:-} == --info ]]; then
    printf 'VPS 项目 ID：%s\n服务账号：%s\n访问范围：\n%s\n' "${vm_project:-未获取，请确认运行在 GCE 虚拟机中}" "$(metadata instance/service-accounts/default/email)" "$(metadata instance/service-accounts/default/scopes)"
    echo '需要服务账号、cloud-platform 范围，以及查询项目的 BigQuery 作业用户和账单数据集的数据查看者权限。'
    exit 0
fi
[[ $# -eq 0 ]] || fail '仅支持无参数或 --info。'
[[ $EUID -eq 0 ]] || fail '请用 sudo bash 执行此脚本。'
[[ -d /run/systemd/system ]] || fail '需要 systemd。'
[[ -x /usr/local/bin/traffic-review ]] || fail '请先升级面板并运行 deploy/setup-traffic-review.sh。'
systemctl cat traffic-review-bill-sync.service >/dev/null 2>&1 || fail '尚未安装账单服务，请先运行 deploy/setup-traffic-review.sh。'
monitor=$(systemctl show traffic-review-bill-sync.service -p User --value)
[[ -n $monitor && $monitor != root ]] || fail '账单服务必须使用独立的非 root 采集用户。'
monitor_group=$(id -gn "$monitor")
[[ -f /var/lib/s-ui-traffic-review/review.db ]] || fail '尚无采集数据库，请先启动采集器。'
echo '请先在 GCP 开启标准使用费用导出，并给 VPS 服务账号授权。无需下载 JSON 密钥。'
read -r -p "查询项目 ID [${vm_project}]：" query_project
query_project=${query_project:-$vm_project}
read -r -p "账单数据集所在项目 ID [${query_project}]：" dataset_project
dataset_project=${dataset_project:-$query_project}
read -r -p '账单数据集 ID（例如 billing_export）：' dataset
read -r -p '完整表名（gcp_billing_export_v1_ 开头，不含项目和数据集）：' table
read -r -p "需要统计的 VPS 项目 ID [${vm_project:-$query_project}]：" target_project
target_project=${target_project:-${vm_project:-$query_project}}
for project in "$query_project" "$dataset_project" "$target_project"; do
    [[ $project =~ ^[a-z][a-z0-9-]{4,29}$ ]] || fail '项目 ID 格式错误；请填写 ID，不是项目名称或编号。'
done
[[ $dataset =~ ^[A-Za-z0-9_]{1,128}$ ]] || fail '数据集 ID 格式错误。'
[[ $table =~ ^gcp_billing_export_v1_[A-Za-z0-9_]{6,64}$ ]] || fail '仅支持标准使用费用导出的完整表名。'
args=(sync-bill --db /var/lib/s-ui-traffic-review/review.db --query-project "$query_project" --dataset-project "$dataset_project" --dataset "$dataset" --table "$table" --target-project "$target_project" --lookback-days 7 --max-bytes 67108864 --max-monthly-bytes 4160749568 --adc)
error_file=$(mktemp)
env_file=''
cleanup() { rm -f "$error_file"; [[ -z $env_file ]] || rm -f "$env_file"; }
trap cleanup EXIT
run_sync() {
    if ! runuser -u "$monitor" -- /usr/local/bin/traffic-review "${args[@]}" "$@" >"$error_file" 2>&1; then
        echo '查询失败：403 请检查服务账号 IAM 和 cloud-platform 范围；404 请检查数据集和表名；扫描超限请查看教程。' >&2
        cat "$error_file" >&2
        return 1
    fi
}
echo '正在预检权限、表和查询扫描上限……'
run_sync --dry-run || fail '预检未通过；原配置和定时器未修改。'
echo '正在同步最近 7 天账单……'
run_sync || fail '首次同步未完成；原配置和定时器未修改。'
install -d -m 0750 -o root -g "$monitor_group" /etc/s-ui
env_file=$(mktemp /etc/s-ui/.billing-env.XXXXXX)
printf 'GCP_QUERY_PROJECT=%s\nGCP_DATASET_PROJECT=%s\nGCP_BILLING_DATASET=%s\nGCP_BILLING_TABLE=%s\nGCP_TARGET_PROJECT=%s\n' "$query_project" "$dataset_project" "$dataset" "$table" "$target_project" >"$env_file"
chown root:"$monitor_group" "$env_file"
chmod 0640 "$env_file"
mv -f "$env_file" /etc/s-ui/traffic-review-billing.env
env_file=''
systemctl daemon-reload
systemctl enable --now traffic-review-bill-sync.timer
echo '完成：已导入首批数据，并开启每 12 小时同步。导出可能延迟；空账单不代表零费用。'
echo '面板 → 出站流量 → GCP 账单：选洛杉矶时间和已导出的日期。'
echo '查看定时器：systemctl list-timers traffic-review-bill-sync.timer --all'
