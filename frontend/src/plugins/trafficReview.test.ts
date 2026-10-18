import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTrafficReviewPoller, TRAFFIC_REVIEW_REFRESH_MS } from './trafficReview'
import type { TrafficReviewQuery, TrafficReviewResponse } from '@/types/trafficReview'

describe('traffic review polling', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('does not overlap refreshes and ignores responses after unmount', async () => {
    const pending: Array<(value: TrafficReviewResponse) => void> = []
    const request = vi.fn(() => new Promise<TrafficReviewResponse>((resolve) => pending.push(resolve)))
    const poller = createTrafficReviewPoller(request, () => 123)
    poller.start()
    expect(request).toHaveBeenCalledTimes(6)
    await vi.advanceTimersByTimeAsync(TRAFFIC_REVIEW_REFRESH_MS)
    expect(request).toHaveBeenCalledTimes(6)
    poller.stop()
    for (const resolve of pending) resolve({ success: true, msg: '', obj: { bytes: 5 } })
    await Promise.resolve()
    expect(poller.states.summary.data).toBeNull()
    await vi.advanceTimersByTimeAsync(TRAFFIC_REVIEW_REFRESH_MS)
    expect(request).toHaveBeenCalledTimes(6)
  })

  it('retains the last good sample and marks an endpoint stale after an error', async () => {
    let failed = false
    const request = vi.fn(async (): Promise<TrafficReviewResponse> =>
      failed ? { success: false, msg: 'temporarily unavailable', obj: null } :
        { success: true, msg: '', obj: { ruleVersion: 'v1', proxyUploadBytes: 1 } }
    )
    const poller = createTrafficReviewPoller(request, () => 123)
    await poller.refresh()
    expect(poller.states.summary.updatedAt).toBe(123)
    failed = true
    await poller.refresh()
    expect(poller.states.summary.data).toMatchObject({ ruleVersion: 'v1', proxyUploadBytes: 1 })
    expect(poller.states.summary.updatedAt).toBe(123)
    expect(poller.states.summary.error).toBe('temporarily unavailable')
  })

  it('discards old-day responses and immediately loads all endpoints for the new day', async () => {
    const pending: Array<(value: TrafficReviewResponse) => void> = []
    const calls: Array<{ endpoint: string, query: TrafficReviewQuery }> = []
    const request = vi.fn((endpoint: string, query: TrafficReviewQuery) => {
      calls.push({ endpoint, query })
      return new Promise<TrafficReviewResponse>((resolve) => pending.push(resolve))
    })
    const poller = createTrafficReviewPoller(request)
    poller.start()
    poller.setQuery({ date: '2026-10-08', timezone: 'America/Los_Angeles' })
    expect(poller.states.summary.data).toBeNull()
    for (const resolve of pending.slice(0, 6)) resolve({ success: true, msg: '', obj: { ruleVersion: 'old' } })
    for (let i = 0; i < 5; i++) await Promise.resolve()
    expect(request).toHaveBeenCalledTimes(12)
    expect(calls.slice(6).every(({ query }) => query.date === '2026-10-08')).toBe(true)
    expect(poller.states.summary.data).toBeNull()
    for (const resolve of pending.slice(6)) resolve({ success: true, msg: '', obj: { ruleVersion: 'new' } })
    for (let i = 0; i < 5; i++) await Promise.resolve()
    expect(poller.states.summary.data).toMatchObject({ ruleVersion: 'new' })
    poller.stop()
  })
})
