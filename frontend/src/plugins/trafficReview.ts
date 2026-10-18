import HttpUtils from '@/plugins/httputil'
import { reactive } from 'vue'
import { adaptTrafficReviewData } from './trafficReviewAdapter'
import { trafficRangeError } from './trafficReviewRange'
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
  let queryValid = true

  async function refresh(): Promise<void> {
    if (!active || loading || !queryValid) return
    loading = true
    const requestedRevision = revision
    const requestedQuery = { ...query }
    try {
      await Promise.all(trafficReviewEndpoints.map(async (endpoint) => {
        try {
          const windowQuery = { date: requestedQuery.date, startDate: requestedQuery.startDate, endDate: requestedQuery.endDate, timezone: requestedQuery.timezone }
          const destinationQuery: TrafficReviewQuery = { ...requestedQuery, layer: 'proxy', sort: 'egress', limit: 500 }
          delete destinationQuery.leg
          const result = await request(endpoint, endpoint === 'destinations'
            ? destinationQuery
            : windowQuery)
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
    if (!active || (query.date ?? '') === (next.date ?? '') && (query.startDate ?? '') === (next.startDate ?? '') && (query.endDate ?? '') === (next.endDate ?? '') && query.timezone === next.timezone && (query.leg ?? '') === (next.leg ?? '')) return
    query = { ...next }
    queryValid = !trafficRangeError(next.startDate, next.endDate, next.timezone)
    revision++
    for (const endpoint of trafficReviewEndpoints) {
      states[endpoint].data = null
      states[endpoint].updatedAt = null
      states[endpoint].error = null
    }
    if (!queryValid) pendingRefresh = false
    else if (loading) pendingRefresh = true
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
