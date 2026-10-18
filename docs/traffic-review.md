# 本地流量复核：部署与证据边界

此功能用于**从启用时起**观察 VPS 的接口字节量、代理会话和内核连接，并单独保存项目/日期/SKU 账单，支持人工 CSV 和可选 BigQuery Standard export 同步。它不改变代理路由、防火墙或现有面板统计。旧 sing-box 日志只有连接记录时，不能追溯每个域名的字节数；账单金额也不能凭日志条数精确分摊给 Telegram 或任一域名。业务域名、IP 注册归属和 `direct` 出站均不能证明 Google 将对应字节计入 Standard 或 Carrier Peering SKU。完整架构图、逐步部署命令和回滚见 [VPS 部署指南](vps-deployment.zh-CN.md)。

费用解释尤其要区分 SKU：Standard 的 200 GiB 免费额度不能当成所有网络出站的统一抵扣。官方说明 CDN Interconnect 出站也可能显示为 `Network Data Transfer Out via Carrier Peering Network`，因此仅凭该 SKU 名称不能反推用户手工配置了中转，亦不能确定具体域名。参见 [VPC 网络价格](https://cloud.google.com/vpc/network-pricing)、[CDN Interconnect 价格](https://cloud.google.com/network-connectivity/pricing#cdn-interconnect-pricing)；账单按 [太平洋时区并遵循夏令时](https://docs.cloud.google.com/billing/docs/how-to/reports)划日。价格说明于 2026-10-18 核查，程序不固化费率或免费额度推断。

实现分三层，各层数值**不可直接相加**。内核逐连接归因范围为 TCP/UDP；其他协议、未经过 conntrack 的流量及协议开销仍只进入网卡总量。UDP 关联可能复用多个目的地，因此代理 UDP 的初始域名只作上下文，业务置信度保留为未知：

1. 面板 SessionTracker 读取活跃 TCP/UDP 会话的累计代理载荷字节，并在短连接关闭时保留最终计数。它按已提供或嗅探的域名标注业务，既不是网卡线速字节，也不能涵盖 VPS 上所有非代理进程。十秒批量写入独立 SQLite，不调用会清零的原 `GetStats`。
2. 独立采集器读取指定公网接口的 RX/TX 内核累计计数，构成接口总量；另用单条固定 `conntrack -E -e NEW,DESTROY -o timestamp,extended,id` 事件流监听 IPv4/IPv6（事件模式不传 `-f`），并分别读取两族活跃快照。原始与回复元组、NAT 及流量方向用于区分 **VPS→网站** 与 **VPS→客户端** 两段；两段都可能形成 VPS 出站。方向无法确定时保留缺口，不猜测目的地或收费类别。内核未启用 `nf_conntrack_acct`、缺少 CLI/权限、UDP 关联丢失或事件缓冲溢出时，逐目的字节可能缺失；采集仍应保留接口总量和健康状态。
3. 本地 CSV 仅记录项目/洛杉矶账单日/SKU 的用量和精确十进制费用文本。即使与本地字节同日出现，也不自动将某条流确认或分摊到某个 SKU。

## 构建与本地试运行

源树已将前端内容并入仓库，便于从一个源码快照构建；对应父基线为 `21866906a0ba260a829366ad56881b6b58d199af`，前端基线为 `abd7f342f6f6dcb626bcf70a25b092f4727a4f2d`。CLI 构建命令为：

```sh
go build -o traffic-review ./cmd/traffic-review
```

面板连同前端可用 `SUI_BUILD_PROFILE=test ./build.sh` 构建：此 profile 包含常规协议、排除需要专门 cronet 工具链的 Naive。正式 Naive 构建继续使用官方 release profile/工具链。Linux 发布归档也包含采集器与部署说明；本次没有发布新版本或替换已有下载地址。

以下是**供管理员审阅后在目标机执行**的命令，当前仓库交付不等于已在 VPS 部署。为防止误用面板数据库，`--db` 必须指向单独路径。`ens4` 是示例接口，应先确认目标机的公网接口名。

```sh
./traffic-review collect --db /var/lib/s-ui-traffic-review/review.db --interface ens4 --interval 10s --conntrack --duration 30s
./traffic-review health --db /var/lib/s-ui-traffic-review/review.db
./traffic-review collect --db /var/lib/s-ui-traffic-review/review.db --interface ens4 --interval 10s --conntrack
```

`--duration 30s` 只适合有限时长检查；正式运行可参照 [`deploy/traffic-review-collector.service`](../deploy/traffic-review-collector.service) 单独托管。采集器无 HTTP 监听口。示例 unit 以 `sui-monitor` 用户和仅 `CAP_NET_ADMIN` 运行，限制系统写权限并将标准输出丢弃、标准错误留在本机文件；部署前须创建该用户、安装 `conntrack`、确认 systemd 版本支持 `StandardError=append:`，并确认本地日志未被 Google Ops Agent 或其他转发器收集。VPS 上现有 Google Ops Agent 保持原样，不因本功能自动停用。不要把流明细、账单或采集器日志导出到云日志。

面板本身的日志是另一条路径：非容器模式默认尝试写 syslog。如管理员选择本地文件记录，可在**现有面板服务**显式设置 `SUI_LOG_STDERR=true`，再由服务管理器将面板 stderr 重定向到受限的本地文件；这不是采集器 unit 自动修改的内容，也不改变现有 Google Ops Agent 的运行状态。需单独核查 Ops Agent 的日志采集规则，避免它再把该本地文件或 syslog 内容上传。

采集器与面板须访问**同一个独立 SQLite 文件及其 WAL/SHM 同目录文件**。示例的 `UMask=0077` 使文件仅归属用户可读写；若面板按原有 root 身份运行，它可以读取。若面板改以非 root 运行，应使两进程采用相同 UID 并授予目录读写权限，不能只依赖同组权限。面板设置 `SUI_TRAFFIC_REVIEW_DB=/var/lib/s-ui-traffic-review/review.db` 后才启用该只读界面；可选 `SUI_TRAFFIC_REVIEW_RULES=/etc/s-ui/traffic-review-rules.json`。独立采集器使用同一规则文件时需显式传 `--rules /etc/s-ui/traffic-review-rules.json`。无规则时费用状态默认为 `unknown`；示例规则中的 `carrier_candidate` / `standard_candidate` 只是管理员假设，绝非账单确认。

面板提供 Cookie 登录后的 `GET /api/traffic-review/{summary,series,destinations,alerts,health,billing}` 六个只读接口。API token 单独不能访问，亦无通过该界面写入规则或账单的端点。时间查询接受 `date=YYYY-MM-DD&timezone=America/Los_Angeles`，或 `from`/`to` Unix 秒；单次最多 31 日，列表 `limit` 为 1–500。账单日始终按洛杉矶日历处理，特别留意 PDT/PST 切换；不要把 UTC 或北京时间自然日当成同一个账单窗口。

## 本地账单导入

先核验来自账单导出的项目、SKU、用量、币种和日期，再手工规范化为 UTF-8 CSV；允许 UTF-8 BOM，列名及顺序必须严格为：

```csv
date,project_id,sku_id,description,usage_gib,cost,currency
```

[`deploy/traffic-review-bill.example.csv`](../deploy/traffic-review-bill.example.csv) 仅含虚构项目。`usage_gib` 和 `cost` 是有限、非科学计数法的十进制文本（最多 12 位小数）；费用保留原精度，不用浮点运算。导入命令：

```sh
./traffic-review import-bill --db /var/lib/s-ui-traffic-review/review.db --file /secure/local/normalized-bill.csv
```

文件最多 4 MiB、最多 10,000 行。数据键为日期、项目、SKU、币种；完全相同的再导入不会产生新账单行，冲突行会使**整批事务拒绝**，不会覆盖已有记录。不同币种分别保存，不混算。导入审计记录本地源文件的 SHA-256、插入行数和 `project_day_sku` 范围。SHA 只能证明导入文件的身份，**不能自动验证其来自真实 Google 账单**；人工核验来源和口径仍必需，更不能因此确认每流费用。

## 五项网络账单描述的分项统计、汇总总额与自动账单同步（Standard Usage Cost Export）

根据 [Google Cloud VPC 网络定价](https://cloud.google.com/vpc/network-pricing)、[Standard Egress](https://cloud.google.com/skus/sku-groups/network-standard-egress)、[Inter-zone](https://cloud.google.com/skus/sku-groups/network-egress-gce)、[Inter-region](https://cloud.google.com/skus/sku-groups/network-egress-inter-region-gce) 与 [Carrier Peering](https://cloud.google.com/skus/sku-groups/interconnect)，分类、展示和同步以具备可读意义的**账单项目描述（`description`）**为主标识（`SKU ID` 仅作为次要参考元数据保留）。系统同时提供**五项汇总总额（`targetTotalUsageGiB` / `targetNetCost`）**与**逐项分类明细**——汇总总额方便估算整体网络流量规模（如样例合计 `79.35 GiB` / `$0.82`），而计算 Standard Tier 200 GiB 免费额度时只抵扣第一项 Standard Internet 出站：

| 账单项目描述（主标识） | 参考 SKU ID | 分类与计费处理方式 |
| --- | --- | --- |
| `Network Standard Data Transfer Out to Internet from Oregon` | `4986-FDE4-A69A` | 计入目标 Oregon VM 的 Standard Tier Internet egress 统计（可对照 200 GiB 参考线，但标注“仅目标 VM”，不代表整个 Billing Account 跨项目/跨区域全局已用量） |
| `Network Inter Zone Data Transfer Out` | `DE9E-AFBC-A15A` | 内部跨 Zone 流量单独备查，计入五项总流量估算，**不计入** Standard Tier 200 GiB 免费额度 |
| `Network Inter Region Data Transfer Out from Americas to Las Vegas` | `ACC0-9F23-6029` | 跨 Region 流量单独备查，计入五项总流量估算，**不计入** Standard Tier 200 GiB 免费额度 |
| `Network Inter Region Data Transfer Out from Americas to Virginia` | `C863-37DA-506E` | 跨 Region 流量单独备查，计入五项总流量估算，**不计入** Standard Tier 200 GiB 免费额度 |
| `Network Data Transfer Out via Carrier Peering Network - Americas Based` | `984A-1F27-2D1F` | 单独记录实际收费 Carrier Peering 用量及金额，计入五项总流量估算，**不可被** Standard Tier 200 GiB 免费额度抵扣 |

自动同步采用 [Standard usage cost export](https://docs.cloud.google.com/billing/docs/how-to/export-data-bigquery-tables/standard-usage)（表名 `gcp_billing_export_v1_<BILLING_ACCOUNT_ID>`），不启用 FOCUS、Detailed usage cost 或 Pricing export；由 VPS 本地 systemd timer（[`deploy/traffic-review-bill-sync.timer`](../deploy/traffic-review-bill-sync.timer) / [`deploy/traffic-review-bill-sync.service`](../deploy/traffic-review-bill-sync.service)）每 12 小时执行一次，保留上述 CSV 手工导入作为备用：

```sh
# 离线模拟快照验证（不连接 GCP）
./traffic-review sync-bill --db /var/lib/s-ui-traffic-review/review.db --mock-export deploy/traffic-review-standard-export.example.json

# 正式启用前先执行 BigQuery Dry Run 检查扫描量与存储上限
./traffic-review sync-bill --db /var/lib/s-ui-traffic-review/review.db \
  --query-project <QUERY_PROJECT_ID> --dataset-project <DATASET_PROJECT_ID> \
  --dataset <BILLING_DATASET> --table gcp_billing_export_v1_<BILLING_ACCOUNT_ID> \
  --target-project <TARGET_PROJECT_ID> --lookback-days 7 --max-bytes 67108864 --max-monthly-bytes 4160749568 --adc --dry-run

# 输出 Carrier Peering 相关性候选、独立中转路由建议及中转前后账单对比
./traffic-review analyze-candidates --db /var/lib/s-ui-traffic-review/review.db --date 2026-10-17 --timezone America/Los_Angeles
```

预算与安全边界：
- **本工具查询预算**：单次 `maximumBytesBilled` 默认 `64 MiB`，本地月度累计上限 `3.875 GiB`，按 [BigQuery 公开按需价 $6.25/TiB](https://cloud.google.com/bigquery/pricing) 估算查询约 `$0.0237/月`。执行前在 SQLite 中串行预留完整单次上限；成功后记录实际 billed bytes，超时/失败/中断保留保守预留，跨进程并发不会重复占用同一份预算。源账单**表**的存储量检查上限为 `10 GiB`，并非整个数据集；它只暂停本工具读取，不会阻止云端表继续增长。账户其他查询量不会自动发现，可通过 `--existing-monthly-bytes` 提供额外保守预留。此保护不保证账户全部查询、存储或其他服务总账单小于 `$0.05/月`，也不自动提高预算。
- **幂等更新、延迟与更正**：Google 账单导出常有 `>24 小时` 延迟，首次启用回填最多可能需要 5 天。同步按 `(date, project_id, sku_id, currency)` 幂等重算回溯窗口：完全相同的 12 小时轮询不会重复累加金额；迟到账单或历史冲正（`export_time` 更新且数值变化）会原地替换为最新日汇总并记录 `bill_correction` 审计告警；缺失或待更新窗口保持 `dataPending=true`，不把缺数据当成 `$0`。
- **认证边界**：仅使用 VM 服务账号 ADC（授予查询项目 `roles/bigquery.jobUser` 与账单数据集 `roles/bigquery.dataViewer`），拒绝长期 JSON 私钥文件。
- **Carrier Peering 站点候选与中转槽位（默认保留待配置）**：由于 Google 账单不包含逐域名/逐连接映射，候选分析仅提供基于时间窗、字节量、代理用户与出站路由的相关性证据（`flowBillingConfirmed` 始终为 `false`）。当前中转节点尚未制定，`RelaySuggestion` 默认保持 `status="pending_configuration"`（`configured=false`、规则出站占位符 `<PENDING_RELAY_OUTBOUND>`），仅针对 `proxy` 或 `to_remote` 远端目标生成候选列表（自动排除 `to_client` 客户端回程 IP）；待后续确定中转节点后，可通过 `--relay-outbound <TAG>` 生成预览规则，并用 `CompareRelayPeriods` 对比中转前后的真实 Carrier Peering 账单变化。

## 质量、容量与验收

首次采集只建立接口和已存在 conntrack 流的**基线**，不补算启用前的流量。采集器/网卡重启、接口更换或计数器回退会产生缺口；丢失的短流、UDP 关联和 ENOBUFS 事件不能靠下一张活跃快照完整恢复。`health`、`alerts`、`summary.partial` 与 `flowBillingConfirmed=false` 应随报表一起查看。代理载荷字节、内核流字节与网卡总量存在不同协议层开销、方向和进程范围；比较差异时不能强行将差额指派到域名。

采集代码保留 7 天的十秒网卡样本和逐目的明细、90 天的分钟网卡总量与告警；连接水位保留 2 小时。逐目的记录最多 100,000 行，告警最多 10,000 条，超限会删最旧数据并报告缺口。SQLite `max_page_count=65536`：在常见 4 KiB 页下主库上限约 256 MiB；WAL/SHM、日志与未完成的检查点可能另占磁盘，`journal_size_limit` 不是瞬时磁盘硬上限。例子中的 `MemoryMax=128M` 是进程保护阈值，可能需根据实测调整，并不保证固定 CPU 或无丢样。

上线前应核对面板仍可正常代理和读取原统计、数据库权限、网卡名、`conntrack` 权限与 `nf_conntrack_acct` 的当前值。如需开启内核记账，只能由管理员**先记录旧值并显式决定**，本程序和模板不会运行 `sysctl`、改防火墙或安装软件。部署后的 48–72 小时连续观察、异常/重启演练及与账单的对照**尚未完成**；因此目前不能宣称费用归因验证通过。
