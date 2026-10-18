import HttpUtils from '@/plugins/httputil'
import { reactive } from 'vue'
import { adaptTrafficReviewData } from './trafficReviewAdapter'
import {
  emptyTrafficReviewStates,
  trafficReviewEndpoints,
  type TrafficReviewEndpoint,
  type TrafficReviewQuery,
  type TrafficReviewResponse,
  type TrafficReviewStates,
} from '@/types/trafficReview'

export const TRAFFIC_REVIEW_REFRESH_MS = 10_000

export function createTrafficReviewPoller(
  request: (endpoint: TrafficReviewEndpoint, query: TrafficReviewQuery) => Promise<TrafficReviewResponse> =
    (endpoint, query) => HttpUtils.get(`api/traffic-review/${endpoint}`, query, {}, false),
  now: () => number = Date.now,
) {
  const states: TrafficReviewStates = reactive(emptyTrafficReviewStates())
  let active = true
  let loading = false
  let timer: ReturnType<typeof setInterval> | undefined
  let query: TrafficReviewQuery = { timezone: 'America/Los_Angeles' }
  let revision = 0
  let pendingRefresh = false

  async function refresh(): Promise<void> {
    if (!active || loading) return
    loading = true
    const requestedRevision = revision
    const requestedQuery = { ...query }
    try {
      await Promise.all(trafficReviewEndpoints.map(async (endpoint) => {
        try {
          const result = await request(endpoint, endpoint === 'destinations'
            ? { ...requestedQuery, layer: 'network', sort: 'egress', limit: 500 }
            : { date: requestedQuery.date, timezone: requestedQuery.timezone })
          if (!active || requestedRevision !== revision) return
          if (!result.success || result.obj == null) {
            states[endpoint].error = result.msg || 'No data returned'
            return
          }
          const data = adaptTrafficReviewData(endpoint, result.obj)
          if (data === null) {
            states[endpoint].error = 'Invalid response data'
            return
          }
          states[endpoint].data = data
          states[endpoint].updatedAt = now()
          states[endpoint].error = null
        } catch (error) {
          if (active && requestedRevision === revision) states[endpoint].error = String(error)
        }
      }))
    } finally {
      loading = false
      if (active && pendingRefresh) {
        pendingRefresh = false
        void refresh()
      }
    }
  }

  function setQuery(next: TrafficReviewQuery): void {
    if (!active || (query.date ?? '') === (next.date ?? '') && query.timezone === next.timezone && (query.leg ?? '') === (next.leg ?? '')) return
    query = { ...next }
    revision++
    for (const endpoint of trafficReviewEndpoints) {
      states[endpoint].data = null
      states[endpoint].updatedAt = null
      states[endpoint].error = null
    }
    if (loading) pendingRefresh = true
    else if (timer) void refresh()
  }

  function start(): void {
    if (!active || timer) return
    void refresh()
    timer = setInterval(() => { void refresh() }, TRAFFIC_REVIEW_REFRESH_MS)
  }

  function stop(): void {
    active = false
    if (timer) clearInterval(timer)
    timer = undefined
  }

  return { states, refresh, setQuery, start, stop, isLoading: () => loading }
}
