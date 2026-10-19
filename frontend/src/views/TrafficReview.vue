<template>
  <v-container
    fluid
    class="traffic-review"
  >
    <div class="d-flex justify-space-between align-center mb-4 ga-3">
      <div>
        <h1 class="text-headline-small">
          {{ t('pages.trafficReview') }}
        </h1><p class="text-medium-emphasis">
          {{ tr('subtitle') }}
        </p>
      </div>
      <v-btn
        prepend-icon="mdi-refresh"
        variant="tonal"
        :loading="refreshing"
        :disabled="!!rangeError"
        @click="refresh"
      >
        {{ tr('refresh') }}
      </v-btn>
    </div>
    <div class="d-flex flex-wrap ga-3 mb-4">
      <v-text-field
        v-model="selectedStartDate"
        type="date"
        clearable
        hide-details
        :label="tr('startDate')"
        style="max-width:220px"
      />
      <v-text-field
        v-model="selectedEndDate"
        type="date"
        clearable
        hide-details
        :label="tr('endDate')"
        style="max-width:220px"
      />
      <v-select
        v-model="selectedTimezone"
        :items="timezoneOptions"
        hide-details
        :label="tr('timezone')"
        style="max-width:280px"
      />
      <v-btn
        class="align-self-center"
        variant="tonal"
        @click="clearRange"
      >
        {{ tr('latestDay') }}
      </v-btn>
      <span class="align-self-center text-body-medium">{{ tr('rangeHint') }}</span>
    </div>
    <v-alert
      v-if="rangeError"
      type="warning"
      variant="tonal"
      class="mb-3"
      data-testid="range-error"
    >
      {{ tr(rangeError) }}
    </v-alert>
    <v-tabs
      v-model="tab"
      color="primary"
      show-arrows
    >
      <v-tab
        v-for="name in ['overview', 'costs', 'relay', 'audit']"
        :key="name"
        :value="name"
      >
        {{ tr(name) }}
      </v-tab>
    </v-tabs>
    <v-divider class="mb-4" />
    <v-window v-model="tab">
      <v-window-item value="overview">
        <source-card
          endpoint="summary"
          :state="states.summary"
          class="mb-4"
        >
          <v-row>
            <v-col
              cols="12"
              md="6"
            >
              <p>{{ tr('proxyEgressTotal') }}</p>
              <strong
                class="text-display-medium d-block my-2"
                data-testid="proxy-egress"
              >{{ formatBytes(proxyTotal) }}</strong>
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <p>{{ tr('proxyIngressTotal') }}</p>
              <strong
                class="text-display-medium d-block my-2"
                data-testid="proxy-ingress"
              >{{ formatBytes(proxyTotal) }}</strong>
            </v-col>
          </v-row>
          <p class="text-body-small">
            {{ tr('proxyMirrorHint') }}
          </p>
          <div class="d-flex flex-wrap ga-6 mt-4">
            <p data-testid="proxy-upload">
              {{ tr('proxyToRemote') }}: {{ formatBytes(hasProxySamples ? summary?.proxyUploadBytes : null) }}
            </p>
            <p data-testid="proxy-download">
              {{ tr('proxyFromRemote') }}: {{ formatBytes(hasProxySamples ? summary?.proxyDownloadBytes : null) }}
            </p>
            <p>{{ tr('proxyToClient') }}: {{ formatBytes(hasProxySamples ? summary?.proxyDownloadBytes : null) }}</p>
          </div>
          <p
            v-if="!hasProxySamples"
            class="mt-3"
          >
            {{ tr('noProxyRecords') }}
          </p>
        </source-card>
        <v-card
          variant="outlined"
          class="pa-4 mb-4"
          data-testid="chatgpt-analysis"
        >
          <h2 class="text-title-large mb-2">
            {{ tr('chatgptTitle') }}
          </h2>
          <p class="text-body-small">
            {{ tr('chatgptRuleHint') }}
          </p>
          <div class="d-flex flex-wrap ga-8 mt-4">
            <div>
              {{ tr('candidateEgress') }}<strong
                class="d-block text-headline-small"
                data-testid="candidate-egress"
              >{{ formatBytes(candidateEgress) }}</strong>
            </div>
            <div>{{ tr('candidateRemoteUpload') }}<strong class="d-block">{{ formatBytes(candidateUpload) }}</strong></div>
            <div>{{ tr('candidateRemoteDownload') }}<strong class="d-block">{{ formatBytes(candidateDownload) }}</strong></div>
          </div>
          <p class="text-body-small mt-3">
            {{ tr('candidateBoundary') }}
          </p>
          <p
            v-if="analysis?.partial && hasProxySamples"
            class="text-body-small mt-2"
            data-testid="proxy-partial"
          >
            {{ tr('proxyPartial') }}
          </p>
          <p
            v-if="(analysis?.unknownDomainBytes ?? 0) > 0"
            class="text-body-small mt-2"
          >
            {{ tr('unknownDomainTraffic') }}: {{ formatBytes(analysis?.unknownDomainBytes) }}
          </p>
          <v-divider class="my-4" />
          <div class="d-flex flex-wrap ga-8">
            <div>{{ tr('actualCarrierUsage') }}<strong class="d-block">{{ carrierBill?.rowCount ? `${carrierBill.usageGiB ?? '—'} GiB` : '—' }}</strong></div>
            <div>{{ tr('actualCarrierCost') }}<strong class="d-block">{{ carrierBill?.rowCount ? money(carrierBill.netCost, carrierBill.currency) : '—' }}</strong></div>
          </div>
          <v-alert
            type="info"
            variant="tonal"
            class="mt-4"
          >
            {{ tr(`analysisStatuses.${analysisStatus}`) }}
          </v-alert>
          <p class="text-body-small mt-3">
            {{ tr('analysisDays') }}: {{ (comparison?.scopes.length ?? 0) > 1 ? tr('analysisPerScope') : comparison?.pairCount ?? '—' }} · {{ tr('analysisCorrelation') }}: {{ correlationText }}
          </p>
          <p
            class="text-body-small mt-2"
            data-testid="analysis-window"
          >
            {{ tr('analysisWindow') }}: {{ formatAnalysisTime(comparison?.analysisFrom) }} — {{ formatAnalysisTime(comparison?.analysisTo) }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr('analysisHistoryHint') }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr('analysisWindowHint') }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr(`analysisReasons.${analysisReason}`) }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr(`analysisSuggestions.${analysisStatus}`) }}
          </p>
          <v-table
            v-if="(comparison?.scopes.length ?? 0) > 1"
            density="compact"
            class="mt-3"
          >
            <thead><tr><th>{{ tr('project') }}</th><th>{{ tr('actualCarrierCost') }}</th><th>{{ tr('analysisDays') }}</th><th>{{ tr('analysisCorrelation') }}</th></tr></thead>
            <tbody>
              <tr
                v-for="scope in comparison?.scopes"
                :key="`${scope.projectID}/${scope.currency}`"
              >
                <td>{{ scope.projectID }}</td><td>{{ money(scope.carrierNetCost, scope.currency) }}</td><td>{{ scope.pairCount ?? '—' }}</td><td>{{ formatCorrelation(scope.candidateCorrelation) }}</td>
              </tr>
            </tbody>
          </v-table>
        </v-card>
        <source-card
          endpoint="destinations"
          :state="states.destinations"
          class="mb-4"
        >
          <h2 class="text-title-large mb-3">
            {{ tr('proxyPeers') }}
          </h2>
          <v-alert
            v-if="summary?.destinationDetailsPartial"
            type="info"
            variant="tonal"
            class="mb-3"
            data-testid="detail-retention"
          >
            {{ tr('detailRetention') }}
          </v-alert>
          <v-table
            v-if="peers.length"
            density="compact"
            height="400"
            fixed-header
          >
            <thead><tr><th>{{ tr('domain') }}</th><th>{{ tr('destination') }}</th><th>{{ tr('proxyToRemote') }}</th><th>{{ tr('proxyFromRemote') }}</th><th>{{ tr('proxyEgressTotal') }}</th></tr></thead>
            <tbody>
              <tr
                v-for="(peer, index) in peers"
                :key="index"
              >
                <td>{{ peer.domain || tr('unknownDomain') }}</td><td>{{ peer.peer || '—' }}</td><td>{{ formatBytes(peer.upBytes) }}</td><td>{{ formatBytes(peer.downBytes) }}</td><td>{{ formatBytes(proxyPeerTotal(peer)) }}</td>
              </tr>
            </tbody>
          </v-table>
          <p v-else>
            {{ tr('noProxyTargets') }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr('proxyTargetNote') }}
          </p>
        </source-card>
      </v-window-item>
      <v-window-item value="costs">
        <v-alert
          v-if="states.summary.error"
          type="warning"
          variant="tonal"
          class="mb-3"
        >
          {{ tr('requestFailed') }}
        </v-alert>
        <h2 class="text-title-large">
          {{ tr('billingTitle') }}
        </h2><p class="mb-3 text-medium-emphasis">
          {{ tr('billingHint') }}
        </p>
        <v-alert
          type="info"
          variant="tonal"
          class="mb-4"
        >
          {{ tr(billingStatusKey(overview)) }} · {{ tr('billingDelay') }}
        </v-alert>
        <v-card
          variant="outlined"
          class="pa-4 mb-4"
        >
          <div class="d-flex flex-wrap ga-8 mb-4">
            <div>{{ tr('billUsage') }}<strong class="d-block text-headline-small">{{ hasBills ? `${overview?.totals.targetTotalUsageGiB ?? '—'} GiB` : '—' }}</strong></div><div>{{ tr('billCost') }}<strong class="d-block text-headline-small">{{ hasBills ? money(overview?.totals.targetNetCost, overview?.totals.currency) : '—' }}</strong></div>
          </div>
          <v-table
            v-if="hasBills"
            density="compact"
          >
            <thead><tr><th>{{ tr('item') }}</th><th>{{ tr('usage') }}</th><th>{{ tr('cost') }}</th></tr></thead><tbody>
              <tr
                v-for="bucket in overview?.targetSKUs"
                :key="bucket.skuID"
              >
                <td>{{ categoryLabel(bucket.category) }}</td><td>{{ bucket.rowCount ? `${bucket.usageGiB ?? '—'} GiB` : '—' }}</td><td>{{ bucket.rowCount ? money(bucket.netCost, bucket.currency) : '—' }}</td>
              </tr>
            </tbody>
          </v-table>
          <p v-else>
            {{ tr('billEmpty') }}
          </p><p
            v-if="(overview?.otherSKUsCount ?? 0) > 0"
            class="text-body-small mt-3"
            data-testid="other-bill-items"
          >
            {{ tr('otherBillsHint') }}
          </p><p class="text-body-small mt-3">
            {{ tr('billScope') }}
          </p>
        </v-card>
        <v-expansion-panels
          v-model="billingPanels"
          multiple
          variant="accordion"
          class="mb-4"
        >
          <v-expansion-panel
            :title="tr('syncSetup')"
            value="setup"
          >
            <v-expansion-panel-text>
              <ol class="steps">
                <li
                  v-for="step in ['export', 'permissions', 'vps', 'finish']"
                  :key="step"
                >
                  {{ tr(`syncSteps.${step}`) }}
                </li>
              </ol><p class="mt-3">
                {{ tr('syncCommand') }}
              </p><pre class="command">sudo bash /usr/local/s-ui/deploy/configure-billing.sh</pre><a
                href="https://github.com/JamesAlaHZS/s-ui-gpc-optimize/blob/main/docs/egress-and-billing.zh-CN.md#3-同步-gcp-账单"
                target="_blank"
                rel="noopener noreferrer"
              >{{ tr('exportHelp') }}</a>
            </v-expansion-panel-text>
          </v-expansion-panel>
          <v-expansion-panel
            :title="tr('syncDetails')"
            value="status"
          >
            <v-expansion-panel-text>
              <p>{{ tr('syncMode') }}: {{ tr(syncMode) }}</p><p>{{ tr('lastSync') }}: {{ formatTime(overview?.syncStatus.lastSyncAt) }}</p><p>{{ tr('lastExport') }}: {{ formatTime(overview?.syncStatus.lastExportTime) }}</p><p>{{ tr('queryBudget') }}: {{ formatBytes(overview?.syncStatus.monthlyScannedBytes) }} / {{ formatBytes(overview?.syncStatus.maxMonthlyBytes) }}</p><p class="mt-3">
                {{ tr('quotaTitle') }}: {{ overview?.standardTierReference.targetVMUsedGiB ?? '—' }} GiB
              </p><p class="text-body-small">
                {{ tr('quotaHint') }}
              </p>
            </v-expansion-panel-text>
          </v-expansion-panel>
          <v-expansion-panel
            :title="tr('rawBills')"
            value="rows"
          >
            <v-expansion-panel-text>
              <source-card
                endpoint="billing"
                :state="states.billing"
              >
                <v-table
                  v-if="billing?.length"
                  density="compact"
                  height="400"
                  fixed-header
                >
                  <thead><tr><th>{{ tr('date') }}</th><th>{{ tr('project') }}</th><th>{{ tr('item') }}</th><th>{{ tr('usage') }}</th><th>{{ tr('cost') }}</th></tr></thead><tbody>
                    <tr
                      v-for="(bill, index) in billing"
                      :key="index"
                    >
                      <td>{{ bill.date }}</td><td>{{ bill.projectID }}</td><td>{{ categoryLabel(bill.category) }}<small class="d-block">{{ bill.skuID }}</small></td><td>{{ bill.usageGiB ?? '—' }} GiB</td><td>{{ money(bill.netCost, bill.currency) }}</td>
                    </tr>
                  </tbody>
                </v-table><p v-else>
                  {{ tr('billEmpty') }}
                </p>
              </source-card>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
      </v-window-item>
      <v-window-item value="relay">
        <v-card
          variant="outlined"
          class="pa-4 mb-4"
        >
          <h2 class="text-title-large mb-2">
            {{ tr('relayTitle') }}
          </h2><p>{{ tr('relayExplain') }}</p><p class="mt-2 text-medium-emphasis">
            {{ tr('relayBoundary') }}
          </p><ol class="steps mt-4">
            <li
              v-for="step in ['node', 'outbound', 'rule', 'verify']"
              :key="step"
            >
              {{ tr(`relaySteps.${step}`) }}
            </li>
          </ol>
        </v-card>
        <v-card
          variant="outlined"
          class="pa-4"
        >
          <h2 class="text-title-large mb-3">
            {{ tr('relayPreview') }}
          </h2><v-text-field
            v-model="outboundTag"
            :label="tr('outboundTag')"
          /><v-textarea
            v-model="domainInput"
            :label="tr('domains')"
            :hint="tr('domainsHint')"
            persistent-hint
            rows="3"
          /><pre
            v-if="rule"
            class="command"
          >{{ JSON.stringify(rule, null, 2) }}</pre><p
            v-else
            class="my-3"
          >
            {{ tr('invalidRelay') }}
          </p><v-btn
            :disabled="!rule"
            variant="tonal"
            @click="copyRule"
          >
            {{ tr('copyRule') }}
          </v-btn><span class="ml-3">{{ copyStatus ? tr(copyStatus) : '' }}</span><p class="text-body-small mt-3">
            {{ tr('previewHint') }}
          </p>
        </v-card>
      </v-window-item>
      <v-window-item value="audit">
        <v-expansion-panels
          class="mb-4"
          variant="accordion"
        >
          <v-expansion-panel :title="tr('networkDiagnostics')">
            <v-expansion-panel-text>
              <p data-testid="nic-egress">
                {{ tr('txTotal') }}: {{ formatBytes(summary?.lastSampleAt ? summary.txBytes : null) }}
              </p>
              <p>{{ tr('rxTotal') }}: {{ formatBytes(summary?.lastSampleAt ? summary.rxBytes : null) }}</p>
              <p>{{ tr('toRemote') }}: {{ formatBytes(hasNetworkSamples ? summary?.toRemoteEgressBytes : null) }}</p>
              <p>{{ tr('toClient') }}: {{ formatBytes(hasNetworkSamples ? summary?.toClientEgressBytes : null) }}</p>
              <p>{{ tr('unknownDirection') }}: {{ formatBytes(hasNetworkSamples ? summary?.unknownDirectionEgressBytes : null) }}</p>
              <p>{{ tr('connectionTotal') }}: {{ formatBytes(hasNetworkSamples ? summary?.networkEgressBytes : null) }}</p>
              <p data-testid="egress-difference">
                {{ tr('egressDifference') }}: {{ formatBytes(hasNetworkSamples && summary?.lastSampleAt ? summary.egressDifferenceBytes : null) }}
              </p>
              <p class="text-body-small mt-2">
                {{ tr('connectionHint') }}
              </p>
              <p class="text-body-small">
                {{ tr('differenceHint') }}
              </p>
              <p>{{ tr('firstSample') }}: {{ formatTime(summary?.firstSampleAt) }}</p>
              <p>{{ tr('lastSample') }}: {{ formatTime(summary?.lastSampleAt) }}</p>
              <p v-if="summary?.partial">
                {{ tr('partial') }}
              </p>
              <source-card
                endpoint="series"
                :state="states.series"
                class="mb-4"
              >
                <egress-chart
                  v-if="series?.length"
                  :points="series"
                  :timezone="selectedTimezone"
                  :locale="language"
                  :label="tr('trend')"
                /><p v-else>
                  {{ tr('noMeasurements') }}
                </p>
              </source-card>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
        <source-card
          endpoint="health"
          :state="states.health"
          class="mb-4"
        >
          <template v-if="health">
            <p>{{ tr('collector') }}: {{ tr(!health.enabled ? 'disabled' : !health.available ? 'stopped' : sampleStale ? 'degraded' : 'collecting') }}</p><p>{{ tr('interface') }}: {{ health.networkInterface || '—' }}</p><p>{{ tr('lastSample') }}: {{ formatTime(health.lastSampleAt) }}</p><p>{{ tr('conntrack') }}: {{ tr(health.conntrack.degraded ? 'degraded' : health.conntrack.available ? 'available' : 'unknown') }}</p><p>{{ tr('eventGaps') }}: {{ health.conntrack.eventGaps ?? '—' }} · {{ tr('restarts') }}: {{ health.conntrack.eventRestarts ?? '—' }} · {{ tr('parseErrors') }}: {{ health.conntrack.parseErrors ?? '—' }}</p><v-alert
              v-if="health.conntrack.lastError"
              type="warning"
              variant="tonal"
              class="mt-3"
            >
              {{ tr(connectionErrorKey(health.conntrack.lastError)) }}
            </v-alert>
          </template><p v-else>
            {{ tr('noMeasurements') }}
          </p>
        </source-card>
        <v-card
          variant="outlined"
          class="pa-4 mb-4"
        >
          <h2 class="text-title-large">
            {{ tr('diagnosticCommand') }}
          </h2><pre class="command">sudo systemctl status traffic-review-collector --no-pager
sudo journalctl -u traffic-review-collector -n 40 --no-pager
sudo tail -n 40 /var/lib/s-ui-traffic-review/collector.err
sudo bash /usr/local/s-ui/deploy/setup-traffic-review.sh</pre>
        </v-card>
        <source-card
          endpoint="alerts"
          :state="states.alerts"
        >
          <p class="text-body-small mb-3">
            {{ tr('legacyAlertsHint') }}
          </p>
          <v-list
            v-if="alerts?.length"
            density="compact"
          >
            <v-list-item
              v-for="item in alerts"
              :key="item.id"
              :title="alertLabel(item.kind)"
              :subtitle="formatTime(item.time)"
            />
          </v-list><p v-else>
            {{ tr('noAlerts') }}
          </p>
        </source-card>
      </v-window-item>
    </v-window>
  </v-container>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SourceCard from '@/components/trafficReview/SourceCard.vue'
import EgressChart from '@/components/trafficReview/EgressChart.vue'
import { createTrafficReviewPoller } from '@/plugins/trafficReview'
import { trafficRangeError } from '@/plugins/trafficReviewRange'
import { proxyPeers, proxyPeerTotal, billingStatusKey, connectionErrorKey, relayRule } from '@/plugins/trafficReviewPresentation'
import type { TrafficAlert, TrafficBillingRecord, TrafficDestination, TrafficHealth, TrafficSeriesPoint, TrafficSummary, TrafficReviewQuery } from '@/types/trafficReview'
const { t, te, locale } = useI18n()
const tr = (key: string) => t(`trafficReview.${key}`)
const language = computed(() => locale.value === 'zhHans' ? 'zh-CN' : locale.value === 'zhHant' ? 'zh-TW' : locale.value)
const tab = ref('overview')
const selectedStartDate = ref('')
const selectedEndDate = ref('')
const selectedTimezone = ref<TrafficReviewQuery['timezone']>('America/Los_Angeles')
const rangeError = computed(() => trafficRangeError(selectedStartDate.value, selectedEndDate.value, selectedTimezone.value))
const timezoneOptions = computed(() => ['America/Los_Angeles', 'Asia/Shanghai', 'UTC'].map((value, index) => ({ value, title: tr(['pacific', 'shanghai', 'utc'][index]!) })))
const poller = createTrafficReviewPoller()
const states = poller.states
const summary = computed(() => states.summary.data as TrafficSummary | null)
const hasNetworkSamples = computed(() => (summary.value?.networkSampleCount ?? 0) > 0)
const analysis = computed(() => summary.value?.proxyAnalysis ?? null)
const hasProxySamples = computed(() => analysis.value?.hasObservations ?? (analysis.value?.observations ?? summary.value?.proxySampleCount ?? 0) > 0)
const comparison = computed(() => analysis.value?.billingComparison ?? null)
const proxyTotal = computed(() => !hasProxySamples.value ? null : analysis.value?.forwardedEgressBytes ?? (summary.value?.proxyUploadBytes != null && summary.value.proxyDownloadBytes != null ? summary.value.proxyUploadBytes + summary.value.proxyDownloadBytes : null))
const hasCandidateSamples = computed(() => analysis.value?.hasObservations ?? (analysis.value?.observations ?? 0) > 0)
const candidateUpload = computed(() => hasCandidateSamples.value ? analysis.value?.candidateUploadBytes : null)
const candidateDownload = computed(() => hasCandidateSamples.value ? analysis.value?.candidateDownloadBytes : null)
const candidateEgress = computed(() => hasCandidateSamples.value ? analysis.value?.candidateForwardedEgressBytes : null)
const analysisStatus = computed(() => ['collecting', 'insufficient_data', 'correlated', 'inconclusive'].includes(comparison.value?.status ?? '') ? comparison.value!.status : 'collecting')
const analysisReason = computed(() => comparison.value?.reason && te(`trafficReview.analysisReasons.${comparison.value.reason}`) ? comparison.value.reason : 'no_proxy_data')
const correlationText = computed(() => comparison.value?.scopes.length === 1 ? formatCorrelation(comparison.value.scopes[0]?.candidateCorrelation) : '—')
const series = computed(() => states.series.data as TrafficSeriesPoint[] | null)
const alerts = computed(() => states.alerts.data as TrafficAlert[] | null)
const health = computed(() => states.health.data as TrafficHealth | null)
const billing = computed(() => states.billing.data as TrafficBillingRecord[] | null)
const peers = computed(() => proxyPeers(states.destinations.data as TrafficDestination[] | null))
const overview = computed(() => summary.value?.billingOverview ?? null)
const carrierBill = computed(() => overview.value?.carrierPeeringActual ?? null)
const hasBills = computed(() => overview.value?.targetSKUs.some(bucket => bucket.rowCount > 0) ?? false)
const billingPanels = ref<string[]>(['setup'])
const syncMode = computed(() => ({ offline_snapshot: 'syncOffline', standard_export_12h: 'syncBigQuery', standard_export_daily: 'syncBigQuery', manual_csv_fallback: 'syncManual' })[overview.value?.syncStatus.mode ?? ''] ?? 'unknown')
const sampleStale = computed(() => !selectedStartDate.value && !selectedEndDate.value && !!health.value?.lastSampleAt && Date.now() - health.value.lastSampleAt * 1000 > Math.max(30_000, (health.value.samplingSeconds ?? 10) * 3_000))
const refreshing = ref(false)
const outboundTag = ref('relay-out')
const domainInput = ref('')
const rule = computed(() => relayRule(outboundTag.value, domainInput.value))
const copyStatus = ref('')
function formatBytes(value: number | null | undefined): string {
  if (value == null) return '—'
  if (Math.abs(value) >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(3)} GiB`
  if (Math.abs(value) >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(2)} MiB`
  return `${(value / 1024).toFixed(2)} KiB`
}
function money(value: string | null | undefined, currency?: string) { return value == null ? currency === 'MIXED' ? tr('currencyMixed') : '—' : `${currency || ''} ${value}`.trim() }
function formatCorrelation(value?: number | null) { return value == null ? '—' : value.toFixed(2) }
function formatAnalysisTime(value?: number | null) { return value ? new Intl.DateTimeFormat(language.value, { dateStyle: 'short', timeStyle: 'short', timeZone: 'America/Los_Angeles' }).format(new Date(value * 1000)) : '—' }
function formatTime(value: number | null | undefined): string { return value ? new Intl.DateTimeFormat(language.value, { dateStyle: 'short', timeStyle: 'medium', timeZone: selectedTimezone.value }).format(new Date(value * 1000)) : '—' }
function categoryLabel(category?: string) { return tr(`skuCategories.${category && te(`trafficReview.skuCategories.${category}`) ? category : 'other_sku'}`) }
function alertLabel(kind: string) { return tr(`alerts.${te(`trafficReview.alerts.${kind}`) ? kind : 'other'}`) }
async function copyRule() { if (!rule.value) return; try { await navigator.clipboard.writeText(JSON.stringify(rule.value, null, 2)); copyStatus.value = 'copied' } catch { copyStatus.value = 'copyFailed' } }
async function refresh() { if (rangeError.value || refreshing.value || poller.isLoading()) return; refreshing.value = true; try { await poller.refresh() } finally { refreshing.value = false } }
function clearRange() { selectedStartDate.value = ''; selectedEndDate.value = '' }
watch([selectedStartDate, selectedEndDate, selectedTimezone], ([startDate, endDate, timezone]) => poller.setQuery({ ...(startDate || endDate ? { startDate: startDate || '', endDate: endDate || '' } : {}), timezone }))
watch([outboundTag, domainInput], () => { copyStatus.value = '' })
onMounted(() => poller.start())
onUnmounted(() => poller.stop())
</script>
<style scoped>
.traffic-review { max-width: 1440px; }
.command { white-space: pre-wrap; overflow-wrap: anywhere; background: rgb(var(--v-theme-on-surface), .05); border-radius: 8px; padding: 16px; margin: 12px 0; }
.steps { padding-left: 24px; }
.steps li + li { margin-top: 12px; }
</style>
