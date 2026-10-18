<template>
  <v-container
    fluid
    class="traffic-review"
  >
    <div class="d-flex align-center justify-space-between flex-wrap ga-3 mb-4">
      <div>
        <h1 class="text-h5">
          {{ t('pages.trafficReview') }}
        </h1>
        <p class="text-body-2 text-medium-emphasis">
          {{ t('trafficReview.readOnly') }}
        </p>
      </div>
      <v-btn
        prepend-icon="mdi-refresh"
        variant="tonal"
        :loading="refreshing"
        @click="refresh"
      >
        {{ t('trafficReview.refresh') }}
      </v-btn>
    </div>

    <div class="d-flex flex-wrap ga-3 mb-4">
      <v-text-field
        v-model="selectedDate"
        type="date"
        clearable
        hide-details
        :label="t('trafficReview.billingDate')"
        style="max-width: 220px"
      />
      <v-select
        v-model="selectedTimezone"
        :items="timezoneOptions"
        hide-details
        :label="t('trafficReview.timezone')"
        style="max-width: 260px"
      />
      <span class="text-body-2 align-self-center">{{ selectedDate ? t('trafficReview.selectedDay') : t('trafficReview.latestDay') }}</span>
    </div>

    <v-alert
      type="info"
      variant="tonal"
      class="mb-4"
    >
      {{ t('trafficReview.boundary') }}
    </v-alert>

    <v-tabs
      v-model="tab"
      color="primary"
      show-arrows
    >
      <v-tab value="overview">
        {{ t('trafficReview.overview') }}
      </v-tab>
      <v-tab value="categories">
        {{ t('trafficReview.categories') }}
      </v-tab>
      <v-tab value="costs">
        {{ t('trafficReview.costs') }}
      </v-tab>
      <v-tab value="audit">
        {{ t('trafficReview.audit') }}
      </v-tab>
    </v-tabs>
    <v-divider class="mb-4" />

    <v-window v-model="tab">
      <v-window-item value="overview">
        <v-alert
          v-if="health && (!health.enabled || !health.available)"
          type="warning"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.collectorUnavailable') }}
        </v-alert>
        <v-alert
          v-if="summary?.partial"
          type="warning"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.partial') }}
        </v-alert>
        <v-alert
          v-if="sampleStale"
          type="warning"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.sampleStale') }}
        </v-alert>
        <v-row>
          <v-col
            cols="12"
            md="6"
          >
            <source-card
              endpoint="summary"
              :state="states.summary"
            >
              <template v-if="summary && health?.enabled && health.available && summary.lastSampleAt">
                <p class="mb-2">
                  {{ t('trafficReview.range') }}: {{ formatTime(summary.from) }} – {{ formatTime(summary.to) }} ({{ summary.timezone || '—' }})
                </p>
                <p class="mb-2">
                  {{ t('trafficReview.ruleVersion') }}: {{ summary.ruleVersion || t('trafficReview.unknown') }}
                </p>
                <v-row>
                  <v-col cols="6">
                    {{ t('trafficReview.proxyUpload') }}<strong class="d-block">{{ formatBytes(summary.proxyUploadBytes) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.proxyDownload') }}<strong class="d-block">{{ formatBytes(summary.proxyDownloadBytes) }}</strong>
                  </v-col>
                  <v-col cols="12">
                    {{ t('trafficReview.proxyTotal') }}<strong class="d-block">{{ formatBytes(summary.proxyTotalBytes ?? ((summary.proxyUploadBytes ?? 0) + (summary.proxyDownloadBytes ?? 0))) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.networkEgress') }}<strong class="d-block">{{ formatBytes(summary.networkEgressBytes) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.networkIngress') }}<strong class="d-block">{{ formatBytes(summary.networkIngressBytes) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.networkTotal') }}<strong class="d-block">{{ formatBytes(summary.networkTotalBytes ?? ((summary.networkEgressBytes ?? 0) + (summary.networkIngressBytes ?? 0))) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.nicTotal') }}<strong class="d-block">{{ formatBytes(summary.nicTotalBytes ?? ((summary.rxBytes ?? 0) + (summary.txBytes ?? 0))) }}</strong>
                  </v-col>
                </v-row>
                <p class="text-body-2 mt-2">
                  {{ summary.disclaimer }}
                </p>
              </template>
              <p v-else>
                {{ t('trafficReview.noMeasurements') }}
              </p>
            </source-card>
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <source-card
              endpoint="series"
              :state="states.series"
            >
              <v-table
                v-if="series?.length"
                density="compact"
              >
                <thead><tr><th>{{ t('trafficReview.time') }}</th><th>TX</th><th>RX</th><th>{{ t('trafficReview.quality') }}</th></tr></thead>
                <tbody>
                  <tr
                    v-for="(point, index) in series.slice(-12).reverse()"
                    :key="`${point.time}-${index}`"
                  >
                    <td>{{ formatTime(point.time) }}</td><td>{{ formatBytes(point.txBytes) }}</td><td>{{ formatBytes(point.rxBytes) }}</td><td>{{ point.quality || t('trafficReview.unknown') }}</td>
                  </tr>
                </tbody>
              </v-table>
              <p v-else>
                {{ t('trafficReview.noMeasurements') }}
              </p>
            </source-card>
          </v-col>
          <v-col cols="12">
            <source-card
              endpoint="health"
              :state="states.health"
            >
              <p v-if="health">
                {{ t('trafficReview.collector') }}: {{ health.status || t('trafficReview.unknown') }} · {{ t('trafficReview.interface') }}: {{ health.networkInterface || t('trafficReview.unknown') }} · {{ t('trafficReview.lastSample') }}: {{ formatTime(health.lastSampleAt) }}
              </p>
              <p v-else>
                {{ t('trafficReview.noMeasurements') }}
              </p>
            </source-card>
          </v-col>
        </v-row>
      </v-window-item>
      <v-window-item value="categories">
        <v-alert
          type="warning"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.direction') }}
        </v-alert>
        <p class="text-body-2 mb-3">
          {{ t('trafficReview.ruleVersion') }}: {{ summary?.ruleVersion || t('trafficReview.unknown') }}
        </p>
        <source-card
          endpoint="destinations"
          :state="states.destinations"
        >
          <v-table
            v-if="destinations?.length"
            density="compact"
          >
            <thead><tr><th>{{ t('trafficReview.layer') }}</th><th>{{ t('trafficReview.leg') }}</th><th>{{ t('trafficReview.destination') }}</th><th>{{ t('trafficReview.business') }}</th><th>{{ t('trafficReview.confidence') }}</th><th>{{ t('trafficReview.ruleVersion') }}</th><th>{{ t('trafficReview.volume') }}</th><th>{{ t('trafficReview.feeStatus') }}</th></tr></thead>
            <tbody>
              <tr
                v-for="(item, index) in destinations"
                :key="`${item.layer}-${item.peer}-${index}`"
              >
                <td>{{ item.layer }}</td><td>{{ legLabel(item.leg) }}</td>
                <td>
                  {{ item.domain || item.peer || t('trafficReview.unknown') }}<small
                    v-if="item.user || item.outbound"
                    class="d-block"
                  >{{ item.user }} {{ item.outbound }}</small>
                </td>
                <td>{{ item.business || t('trafficReview.unknown') }}</td><td>{{ item.confidence }}</td>
                <td>{{ item.ruleVersion || t('trafficReview.unknown') }}</td>
                <td>{{ item.layer === 'proxy' ? `${formatBytes(item.upBytes)} / ${formatBytes(item.downBytes)}` : `${formatBytes(item.txBytes)} / ${formatBytes(item.rxBytes)}` }}</td>
                <td>
                  {{ feeLabel(item.feeStatus) }}<small
                    v-if="item.feeBasis"
                    class="d-block"
                  >{{ item.feeBasis }}</small>
                </td>
              </tr>
            </tbody>
          </v-table>
          <p v-else>
            {{ t('trafficReview.noMeasurements') }}
          </p>
        </source-card>
      </v-window-item>
      <v-window-item value="costs">
        <v-alert
          type="warning"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.costBoundary') }}
        </v-alert>
        <v-alert
          v-if="summary?.billingOverview?.syncStatus?.dataPending"
          type="info"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.dataPendingWarning') }}
        </v-alert>
        <v-alert
          v-if="summary?.billingOverview?.syncStatus?.budgetPaused"
          type="error"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.budgetPausedWarning') }}
        </v-alert>

        <v-card
          v-if="summary?.billingOverview"
          variant="outlined"
          class="mb-4 pa-4"
        >
          <div class="text-subtitle-1 font-weight-medium mb-2">
            {{ t('trafficReview.standardQuotaTitle') }}
          </div>
          <v-row class="mb-2">
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.targetTotalUsage') }}</span>
              <strong class="d-block">{{ summary.billingOverview.totals.targetTotalUsageGiB ?? '0' }} GiB</strong>
            </v-col>
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.targetTotalNetCost') }}</span>
              <strong class="d-block">{{ summary.billingOverview.totals.currency || 'USD' }} {{ summary.billingOverview.totals.targetNetCost ?? '—' }}</strong>
            </v-col>
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.targetVMStandardUsed') }}</span>
              <strong class="d-block">{{ summary.billingOverview.standardTierReference.targetVMUsedGiB ?? '0' }} / {{ summary.billingOverview.standardTierReference.referenceLimitGiB ?? '200' }} GiB</strong>
            </v-col>
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.targetVMRemainingRef') }}</span>
              <strong class="d-block">{{ summary.billingOverview.standardTierReference.targetVMRemainingRefGiB ?? '200' }} GiB</strong>
            </v-col>
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.excludedTargetSKUs') }}</span>
              <strong class="d-block">{{ summary.billingOverview.totals.nonStandardUsageGiB ?? '0' }} GiB ({{ t('trafficReview.internalReferenceUsed') }}: {{ summary.billingOverview.totals.internalReferenceUsageGiB ?? '0' }} GiB)</strong>
            </v-col>
            <v-col
              cols="12"
              sm="6"
              md="4"
            >
              <span class="text-caption text-medium-emphasis">{{ t('trafficReview.carrierActualBilled') }}</span>
              <strong class="d-block">{{ summary.billingOverview.carrierPeeringActual?.usageGiB ?? '0' }} GiB / {{ summary.billingOverview.carrierPeeringActual?.currency || 'USD' }} {{ summary.billingOverview.carrierPeeringActual?.netCost ?? '—' }}</strong>
            </v-col>
          </v-row>
          <p class="text-body-2 text-medium-emphasis mb-3">
            {{ summary.billingOverview.standardTierReference.note }}
          </p>

          <div class="text-subtitle-2 font-weight-medium mb-2">
            {{ t('trafficReview.fiveSKUTitle') }}
          </div>
          <v-table
            v-if="summary.billingOverview.targetSKUs.length"
            density="compact"
            class="mb-3"
          >
            <thead>
              <tr>
                <th>{{ t('trafficReview.billingDescriptionCol') }}</th>
                <th>{{ t('trafficReview.usage') }}</th>
                <th>{{ t('trafficReview.cost') }}</th>
                <th>{{ t('trafficReview.treatment') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="sku in summary.billingOverview.targetSKUs"
                :key="`${sku.category}-${sku.description}`"
              >
                <td>
                  <strong>{{ sku.description }}</strong>
                  <small
                    v-if="sku.skuID"
                    class="d-block text-medium-emphasis"
                  >SKU: {{ sku.skuID }}</small>
                </td>
                <td>{{ sku.usageGiB ?? '0' }} GiB</td>
                <td>{{ sku.currency || 'USD' }} {{ sku.netCost ?? sku.grossCost ?? '—' }}</td>
                <td>{{ skuHandlingLabel(sku.handling) }}</td>
              </tr>
              <tr class="font-weight-bold">
                <td>{{ t('trafficReview.totalRowLabel') }}</td>
                <td>{{ summary.billingOverview.totals.targetTotalUsageGiB ?? '0' }} GiB</td>
                <td>{{ summary.billingOverview.totals.currency || 'USD' }} {{ summary.billingOverview.totals.targetNetCost ?? '—' }}</td>
                <td>{{ t('trafficReview.totalRowTreatment') }}</td>
              </tr>
            </tbody>
          </v-table>

          <div class="text-subtitle-2 font-weight-medium mb-1">
            {{ t('trafficReview.syncStatusTitle') }}
          </div>
          <p class="text-body-2 text-medium-emphasis mb-0">
            {{ t('trafficReview.syncMode') }}: {{ summary.billingOverview.syncStatus.mode || 'manual_csv_fallback' }} ({{ summary.billingOverview.syncStatus.intervalHours }}h) ·
            {{ t('trafficReview.lastSync') }}: {{ formatTime(summary.billingOverview.syncStatus.lastSyncAt) }} ·
            {{ t('trafficReview.lastExport') }}: {{ formatTime(summary.billingOverview.syncStatus.lastExportTime) }} ·
            {{ t('trafficReview.budgetScan') }}: {{ formatBytes(summary.billingOverview.syncStatus.monthlyScannedBytes) }} / {{ formatBytes(summary.billingOverview.syncStatus.maxMonthlyBytes) }} ·
            {{ t('trafficReview.estimatedCost') }}: ${{ summary.billingOverview.syncStatus.estimatedMonthlyUSD ?? '0' }} / ${{ summary.billingOverview.syncStatus.monthlyBudgetLimitUSD ?? '0.05' }}
          </p>
        </v-card>

        <v-card
          v-if="summary?.carrierCorrelation"
          variant="outlined"
          class="mb-4 pa-4"
        >
          <div class="text-subtitle-1 font-weight-medium mb-1">
            {{ t('trafficReview.carrierCandidatesTitle') }}
          </div>
          <p class="text-body-2 text-medium-emphasis mb-1">
            {{ t('trafficReview.relayStatusLabel') }}:
            <strong>{{ summary.carrierCorrelation.relaySuggestion.configured ? t('trafficReview.relayConfiguredPreview') : t('trafficReview.relayPendingConfig') }}</strong>
          </p>
          <p class="text-body-2 text-medium-emphasis mb-2">
            {{ t('trafficReview.sideBySideComparison') }}:
            Network Data Transfer Out via Carrier Peering Network - Americas Based = {{ summary.carrierCorrelation.billedCarrierPeeringGiB ?? '0' }} GiB ({{ summary.carrierCorrelation.billedCurrency || 'USD' }} {{ summary.carrierCorrelation.billedCarrierPeeringCost ?? '—' }}) ·
            Proxy = {{ summary.carrierCorrelation.localProxyCandidateGiB ?? '0' }} GiB ·
            Network = {{ summary.carrierCorrelation.localNetworkCandidateGiB ?? '0' }} GiB ·
            flowBillingConfirmed = false
          </p>
          <v-table
            v-if="summary.carrierCorrelation.candidates.length"
            density="compact"
            class="mb-2"
          >
            <thead>
              <tr>
                <th>{{ t('trafficReview.layer') }}</th>
                <th>{{ t('trafficReview.leg') }}</th>
                <th>{{ t('trafficReview.destination') }}</th>
                <th>{{ t('trafficReview.volume') }}</th>
                <th>{{ t('trafficReview.confidence') }}</th>
                <th>{{ t('trafficReview.source') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(cand, cIdx) in summary.carrierCorrelation.candidates.slice(0, 10)"
                :key="`${cand.layer}-${cand.destination}-${cIdx}`"
              >
                <td>{{ cand.layer }}</td>
                <td>{{ legLabel(cand.leg) }}</td>
                <td>
                  {{ cand.destination }}
                  <small
                    v-if="cand.user || cand.outbound"
                    class="d-block text-medium-emphasis"
                  >{{ cand.user }} {{ cand.outbound }} → {{ cand.suggestedOutboundTag }}</small>
                </td>
                <td>{{ cand.egressGiB ?? '0' }} GiB ({{ cand.windowSharePercent ?? '0' }}%)</td>
                <td>{{ cand.confidence }} ({{ cand.correlationLevel }})</td>
                <td>
                  <small class="d-block">{{ cand.evidenceBasis }}</small>
                  <small class="d-block text-medium-emphasis">{{ cand.collectionGap }}</small>
                </td>
              </tr>
            </tbody>
          </v-table>
          <div
            v-if="summary.carrierCorrelation.relaySuggestion.singBoxRule"
            class="mt-2"
          >
            <div class="text-caption font-weight-medium mb-1">
              {{ t('trafficReview.relayRulePreview') }}
            </div>
            <pre class="text-caption pa-2 bg-surface-light rounded overflow-x-auto">{{ formatRuleJSON(summary.carrierCorrelation.relaySuggestion.singBoxRule) }}</pre>
          </div>
        </v-card>

        <source-card
          endpoint="billing"
          :state="states.billing"
        >
          <v-table
            v-if="billing?.length"
            density="compact"
          >
            <thead><tr><th>{{ t('trafficReview.date') }}</th><th>{{ t('trafficReview.project') }}</th><th>{{ t('trafficReview.billingDescriptionCol') }}</th><th>{{ t('trafficReview.usage') }}</th><th>{{ t('trafficReview.cost') }}</th><th>{{ t('trafficReview.source') }}</th></tr></thead>
            <tbody>
              <tr
                v-for="(item, index) in billing"
                :key="`${item.date}-${item.projectID}-${item.skuID}-${index}`"
              >
                <td>{{ item.date || '—' }}</td>
                <td>{{ item.projectID || '—' }}</td>
                <td>
                  <strong>{{ item.description || item.skuID || '—' }}</strong>
                  <small
                    v-if="item.skuID"
                    class="d-block text-medium-emphasis"
                  >SKU: {{ item.skuID }}</small>
                </td>
                <td>{{ item.usageGiB == null ? '—' : `${item.usageGiB} GiB` }}</td>
                <td>{{ item.cost == null ? '—' : `${item.currency} ${item.cost}` }}</td>
                <td>
                  {{ item.scope === 'project_day_sku' ? t('trafficReview.billingScope') : t('trafficReview.unknown') }}<small
                    v-if="item.sourceSHA256"
                    class="d-block"
                  >SHA256: {{ item.sourceSHA256 }}</small>
                </td>
              </tr>
            </tbody>
          </v-table>
          <p v-else>
            {{ t('trafficReview.noBillingEvidence') }}
          </p>
        </source-card>
      </v-window-item>
      <v-window-item value="audit">
        <v-alert
          type="info"
          variant="tonal"
          class="mb-4"
        >
          {{ t('trafficReview.auditBoundary') }}
        </v-alert>
        <v-row>
          <v-col
            cols="12"
            md="6"
          >
            <source-card
              endpoint="alerts"
              :state="states.alerts"
            >
              <v-list
                v-if="alerts?.length"
                density="compact"
              >
                <v-list-item
                  v-for="item in alerts"
                  :key="item.id"
                  :title="item.message || item.kind"
                  :subtitle="`${formatTime(item.time)} · ${item.kind} · ${item.ruleVersion || t('trafficReview.unknown')}`"
                />
              </v-list>
              <p v-else>
                {{ t('trafficReview.noAlerts') }}
              </p>
            </source-card>
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <source-card
              endpoint="health"
              :state="states.health"
            >
              <template v-if="health">
                <p>{{ t('trafficReview.collector') }}: {{ health.enabled && health.available ? health.status : t('trafficReview.collectorUnavailable') }}</p>
                <p>Conntrack: {{ health.conntrack.degraded ? t('trafficReview.degraded') : health.conntrack.available ? t('trafficReview.available') : t('trafficReview.unknown') }}</p>
                <p>{{ t('trafficReview.eventGaps') }}: {{ formatCount(health.conntrack.eventGaps) }} · {{ t('trafficReview.restarts') }}: {{ formatCount(health.conntrack.eventRestarts) }} · {{ t('trafficReview.parseErrors') }}: {{ formatCount(health.conntrack.parseErrors) }}</p>
                <p v-if="health.conntrack.lastError">
                  {{ health.conntrack.lastError }}
                </p>
              </template>
              <p v-else>
                {{ t('trafficReview.noMeasurements') }}
              </p>
            </source-card>
          </v-col>
        </v-row>
      </v-window-item>
    </v-window>
  </v-container>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import SourceCard from '@/components/trafficReview/SourceCard.vue'
import { createTrafficReviewPoller } from '@/plugins/trafficReview'
import type { TrafficAlert, TrafficBillingRecord, TrafficDestination, TrafficHealth, TrafficSeriesPoint, TrafficSummary, FeeStatus, TrafficReviewQuery, TrafficDestination as Destination } from '@/types/trafficReview'

const { t } = useI18n()
const tab = ref('overview')
const selectedDate = ref('')
const selectedTimezone = ref<TrafficReviewQuery['timezone']>('America/Los_Angeles')
const timezoneOptions: TrafficReviewQuery['timezone'][] = ['America/Los_Angeles', 'Asia/Shanghai', 'UTC']
const poller = createTrafficReviewPoller()
const states = poller.states
const summary = computed(() => states.summary.data as TrafficSummary | null)
const series = computed(() => states.series.data as TrafficSeriesPoint[] | null)
const destinations = computed(() => states.destinations.data as TrafficDestination[] | null)
const alerts = computed(() => states.alerts.data as TrafficAlert[] | null)
const health = computed(() => states.health.data as TrafficHealth | null)
const billing = computed(() => states.billing.data as TrafficBillingRecord[] | null)
const sampleStale = computed(() => {
  if (!health.value?.enabled || !health.value.available || !health.value.lastSampleAt) return false
  return Date.now() - health.value.lastSampleAt * 1000 > Math.max(30_000, (health.value.samplingSeconds ?? 10) * 3_000)
})
const refreshing = ref(false)

function formatBytes(value: number | null): string {
  if (value == null) return t('trafficReview.unknown')
  return `${(value / (1024 ** 3)).toFixed(3)} GiB`
}
function formatCount(value: number | null): string { return value == null ? t('trafficReview.unknown') : String(value) }
function formatTime(value: number | null): string {
  return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'medium', timeZone: selectedTimezone.value }).format(new Date(value * 1000)) : t('trafficReview.unknown')
}
function feeLabel(value: FeeStatus): string { return t(`trafficReview.fees.${value}`) }
function legLabel(value: Destination['leg']): string { return t(`trafficReview.legs.${value}`) }
function skuHandlingLabel(value: string): string {
  const key = `trafficReview.skuHandling.${value}`
  const res = t(key)
  return res === key ? value : res
}
function formatRuleJSON(value: Record<string, unknown> | null): string {
  return value ? JSON.stringify(value, null, 2) : ''
}

async function refresh() {
  if (refreshing.value || poller.isLoading()) return
  refreshing.value = true
  try { await poller.refresh() } finally { refreshing.value = false }
}

watch([selectedDate, selectedTimezone], ([date, timezone]) => {
  poller.setQuery({ ...(date ? { date } : {}), timezone })
})
onMounted(() => poller.start())
onUnmounted(() => poller.stop())
</script>
