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
        <v-alert
          v-if="health && (!health.enabled || !health.available)"
          type="warning"
          variant="tonal"
          class="mb-3"
        >
          {{ tr('collectorUnavailable') }}
        </v-alert>
        <v-alert
          v-if="summary?.partial && summary.lastSampleAt"
          type="info"
          variant="tonal"
          class="mb-3"
        >
          {{ tr('partial') }}
        </v-alert>
        <v-alert
          v-if="sampleStale"
          type="warning"
          variant="tonal"
          class="mb-3"
        >
          {{ tr('sampleStale') }}
        </v-alert>
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
              <p>{{ tr('txTotal') }}</p><strong
                class="text-display-medium d-block my-2"
                data-testid="nic-egress"
              >{{ formatBytes(summary?.lastSampleAt ? summary.txBytes : null) }}</strong><p class="text-medium-emphasis">
                {{ tr('txTotalHint') }}
              </p>
            </v-col>
            <v-col
              cols="6"
              md="3"
            >
              <p>{{ tr('toRemote') }}</p><strong class="text-headline-small d-block my-2">{{ formatBytes(hasNetworkSamples ? summary?.toRemoteEgressBytes : null) }}</strong>
            </v-col>
            <v-col
              cols="6"
              md="3"
            >
              <p>{{ tr('toClient') }}</p><strong class="text-headline-small d-block my-2">{{ formatBytes(hasNetworkSamples ? summary?.toClientEgressBytes : null) }}</strong>
            </v-col>
          </v-row>
          <p class="mt-3 text-body-medium text-medium-emphasis">
            {{ tr('connectionHint') }}
          </p>
          <p class="mt-2 text-body-medium">
            {{ tr('unknownDirection') }}: {{ formatBytes(hasNetworkSamples ? summary?.unknownDirectionEgressBytes : null) }} · {{ tr('connectionTotal') }}: {{ formatBytes(hasNetworkSamples ? summary?.networkEgressBytes : null) }}
          </p>
          <p
            v-if="!hasNetworkSamples"
            class="text-body-small mt-2"
          >
            {{ tr('noConnectionRecords') }}
          </p>
          <p
            class="mt-2 text-body-medium"
            data-testid="egress-difference"
          >
            {{ tr('egressDifference') }}: {{ formatBytes(hasNetworkSamples && summary?.lastSampleAt ? summary.egressDifferenceBytes : null) }}
          </p>
          <p class="text-body-small">
            {{ tr('differenceHint') }}
          </p>
          <p class="text-body-small mt-2">
            {{ tr('measurementNote') }}
          </p>
        </source-card>
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
        <source-card
          endpoint="destinations"
          :state="states.destinations"
          class="mb-4"
        >
          <v-alert
            v-if="summary?.destinationDetailsPartial"
            type="info"
            variant="tonal"
            class="mb-3"
            data-testid="detail-retention"
          >
            {{ tr('detailRetention') }}
          </v-alert>
          <v-select
            v-model="selectedLeg"
            :items="directionOptions"
            :label="tr('direction')"
            hide-details
            class="mb-3"
            style="max-width:280px"
          />
          <v-table
            v-if="peers.length"
            density="compact"
            height="400"
            fixed-header
          >
            <thead><tr><th>{{ tr('destination') }}</th><th>{{ tr('direction') }}</th><th>{{ tr('protocol') }}</th><th>{{ tr('sent') }}</th></tr></thead><tbody>
              <tr
                v-for="(peer, index) in peers"
                :key="index"
              >
                <td>{{ peer.peer || '—' }}</td><td>{{ legLabel(peer.leg) }}</td><td>{{ peer.protocol || '—' }}</td><td>{{ formatBytes(peer.txBytes) }}</td>
              </tr>
            </tbody>
          </v-table>
          <p v-else>
            {{ tr('noTargets') }}
          </p><p class="text-body-small mt-2">
            {{ tr('targetNote') }}
          </p>
        </source-card>
        <v-expansion-panels variant="accordion">
          <v-expansion-panel :title="tr('advanced')">
            <v-expansion-panel-text>
              <p>{{ tr('rxTotal') }}: {{ formatBytes(summary?.lastSampleAt ? summary.rxBytes : null) }}</p>
              <p data-testid="proxy-upload">
                {{ tr('proxyUpload') }}: {{ formatBytes(hasProxySamples ? summary?.proxyUploadBytes : null) }}
              </p>
              <p data-testid="proxy-download">
                {{ tr('proxyDownload') }}: {{ formatBytes(hasProxySamples ? summary?.proxyDownloadBytes : null) }}
              </p>
              <p
                v-if="!hasProxySamples"
                class="text-body-small mt-2"
              >
                {{ tr('noProxyRecords') }}
              </p>
              <p class="text-body-small mt-2">
                {{ tr('proxyHint') }}
              </p>
              <p>{{ tr('firstSample') }}: {{ formatTime(summary?.firstSampleAt) }}</p>
              <p>{{ tr('lastSample') }}: {{ formatTime(summary?.lastSampleAt) }}</p>
            </v-expansion-panel-text>
          </v-expansion-panel>
          <v-expansion-panel :title="tr('accountingTitle')">
            <v-expansion-panel-text>{{ tr('accountingDetail') }}</v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
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
import { outboundPeers, billingStatusKey, connectionErrorKey, relayRule } from '@/plugins/trafficReviewPresentation'
import type { TrafficAlert, TrafficBillingRecord, TrafficDestination, TrafficHealth, TrafficSeriesPoint, TrafficSummary, TrafficReviewQuery } from '@/types/trafficReview'
const { t, te, locale } = useI18n()
const tr = (key: string) => t(`trafficReview.${key}`)
const language = computed(() => locale.value === 'zhHans' ? 'zh-CN' : locale.value === 'zhHant' ? 'zh-TW' : locale.value)
const tab = ref('overview')
const selectedStartDate = ref('')
const selectedEndDate = ref('')
const selectedTimezone = ref<TrafficReviewQuery['timezone']>('America/Los_Angeles')
const rangeError = computed(() => trafficRangeError(selectedStartDate.value, selectedEndDate.value, selectedTimezone.value))
const selectedLeg = ref<NonNullable<TrafficReviewQuery['leg']>>('')
const timezoneOptions = computed(() => ['America/Los_Angeles', 'Asia/Shanghai', 'UTC'].map((value, index) => ({ value, title: tr(['pacific', 'shanghai', 'utc'][index]!) })))
const directionOptions = computed(() => [{ value: '', title: tr('allDirections') }, { value: 'to_remote', title: tr('toRemote') }, { value: 'to_client', title: tr('toClient') }, { value: 'unknown', title: tr('unknownDirection') }])
const poller = createTrafficReviewPoller()
const states = poller.states
const summary = computed(() => states.summary.data as TrafficSummary | null)
const hasNetworkSamples = computed(() => (summary.value?.networkSampleCount ?? 0) > 0)
const hasProxySamples = computed(() => (summary.value?.proxySampleCount ?? 0) > 0)
const series = computed(() => states.series.data as TrafficSeriesPoint[] | null)
const alerts = computed(() => states.alerts.data as TrafficAlert[] | null)
const health = computed(() => states.health.data as TrafficHealth | null)
const billing = computed(() => states.billing.data as TrafficBillingRecord[] | null)
const peers = computed(() => outboundPeers(states.destinations.data as TrafficDestination[] | null))
const overview = computed(() => summary.value?.billingOverview ?? null)
const hasBills = computed(() => overview.value?.targetSKUs.some(bucket => bucket.rowCount > 0) ?? false)
const billingPanels = ref<string[]>(['setup'])
const syncMode = computed(() => ({ offline_snapshot: 'syncOffline', standard_export_12h: 'syncBigQuery', manual_csv_fallback: 'syncManual' })[overview.value?.syncStatus.mode ?? ''] ?? 'unknown')
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
function formatTime(value: number | null | undefined): string { return value ? new Intl.DateTimeFormat(language.value, { dateStyle: 'short', timeStyle: 'medium', timeZone: selectedTimezone.value }).format(new Date(value * 1000)) : '—' }
function legLabel(leg: string) { return tr(leg === 'to_remote' ? 'toRemote' : leg === 'to_client' ? 'toClient' : 'unknownDirection') }
function categoryLabel(category?: string) { return tr(`skuCategories.${category && te(`trafficReview.skuCategories.${category}`) ? category : 'other_sku'}`) }
function alertLabel(kind: string) { return tr(`alerts.${te(`trafficReview.alerts.${kind}`) ? kind : 'other'}`) }
async function copyRule() { if (!rule.value) return; try { await navigator.clipboard.writeText(JSON.stringify(rule.value, null, 2)); copyStatus.value = 'copied' } catch { copyStatus.value = 'copyFailed' } }
async function refresh() { if (rangeError.value || refreshing.value || poller.isLoading()) return; refreshing.value = true; try { await poller.refresh() } finally { refreshing.value = false } }
function clearRange() { selectedStartDate.value = ''; selectedEndDate.value = '' }
watch([selectedStartDate, selectedEndDate, selectedTimezone, selectedLeg], ([startDate, endDate, timezone, leg]) => poller.setQuery({ ...(startDate || endDate ? { startDate: startDate || '', endDate: endDate || '' } : {}), timezone, leg }))
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
