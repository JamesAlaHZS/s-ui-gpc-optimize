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
                  <v-col cols="6">
                    {{ t('trafficReview.networkEgress') }}<strong class="d-block">{{ formatBytes(summary.networkEgressBytes) }}</strong>
                  </v-col>
                  <v-col cols="6">
                    {{ t('trafficReview.networkIngress') }}<strong class="d-block">{{ formatBytes(summary.networkIngressBytes) }}</strong>
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
        <source-card
          endpoint="billing"
          :state="states.billing"
        >
          <v-table
            v-if="billing?.length"
            density="compact"
          >
            <thead><tr><th>{{ t('trafficReview.date') }}</th><th>{{ t('trafficReview.project') }}</th><th>SKU</th><th>{{ t('trafficReview.usage') }}</th><th>{{ t('trafficReview.cost') }}</th><th>{{ t('trafficReview.source') }}</th></tr></thead>
            <tbody>
              <tr
                v-for="(item, index) in billing"
                :key="`${item.date}-${item.projectID}-${item.skuID}-${index}`"
              >
                <td>{{ item.date || '—' }}</td><td>{{ item.projectID || '—' }}</td><td>{{ item.skuID || '—' }}<small class="d-block">{{ item.description }}</small></td>
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
