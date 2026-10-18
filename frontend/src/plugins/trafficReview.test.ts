import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTrafficReviewPoller, TRAFFIC_REVIEW_REFRESH_MS } from './trafficReview'
import type { TrafficReviewQuery, TrafficReviewResponse } from '@/types/trafficReview'

describe('traffic review polling', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('filters and ranks destinations on the server without narrowing the VPS total', async () => {
    const request = vi.fn<(endpoint: string, query: TrafficReviewQuery) => Promise<TrafficReviewResponse>>(async () => ({ success: true, msg: '', obj: [] }))
    const poller = createTrafficReviewPoller(request)
    poller.setQuery({ timezone: 'UTC', leg: 'to_client' })
    await poller.refresh()
    expect(request).toHaveBeenCalledWith('destinations', { timezone: 'UTC', leg: 'to_client', layer: 'network', sort: 'egress', limit: 500 })
    expect(request).toHaveBeenCalledWith('summary', { timezone: 'UTC', date: undefined, startDate: undefined, endDate: undefined })
    poller.stop()
  })

  it('passes the same inclusive range and timezone to all six endpoints', async () => {
    const request = vi.fn<(endpoint: string, query: TrafficReviewQuery) => Promise<TrafficReviewResponse>>(async () => ({ success: true, msg: '', obj: [] }))
    const poller = createTrafficReviewPoller(request)
    poller.setQuery({ startDate: '2024-01-01', endDate: '2026-10-18', timezone: 'Asia/Shanghai' })
    await poller.refresh()
    expect(request).toHaveBeenCalledTimes(6)
    expect(request.mock.calls.every(([, query]) => query.startDate === '2024-01-01' && query.endDate === '2026-10-18' && query.timezone === 'Asia/Shanghai')).toBe(true)
    poller.stop()
  })

  it('omits an empty direction instead of serializing a rejected leg= parameter', async () => {
    const request = vi.fn<(endpoint: string, query: TrafficReviewQuery) => Promise<TrafficReviewResponse>>(async () => ({ success: true, msg: '', obj: [] }))
    const poller = createTrafficReviewPoller(request)
    poller.setQuery({ startDate: '2026-10-01', endDate: '2026-10-18', timezone: 'UTC', leg: '' })
    await poller.refresh()
    expect(request).toHaveBeenCalledWith('destinations', { startDate: '2026-10-01', endDate: '2026-10-18', timezone: 'UTC', layer: 'network', sort: 'egress', limit: 500 })
    expect(request.mock.calls.find(([endpoint]) => endpoint === 'destinations')?.[1]).not.toHaveProperty('leg')
    poller.stop()
  })

  it('suspends invalid ranges, discards in-flight old results and resumes the latest valid range', async () => {
    const pending: Array<(value: TrafficReviewResponse) => void> = []
    const request = vi.fn(() => new Promise<TrafficReviewResponse>(resolve => pending.push(resolve)))
    const poller = createTrafficReviewPoller(request)
    poller.start()
    poller.setQuery({ startDate: '2026-01-01', endDate: '', timezone: 'UTC' })
    for (const resolve of pending) resolve({ success: true, msg: '', obj: { txBytes: 123 } })
    for (let i = 0; i < 5; i++) await Promise.resolve()
    await vi.advanceTimersByTimeAsync(TRAFFIC_REVIEW_REFRESH_MS)
    expect(request).toHaveBeenCalledTimes(6)
    expect(poller.states.summary.data).toBeNull()
    poller.setQuery({ startDate: '2026-01-01', endDate: '2026-10-18', timezone: 'UTC' })
    expect(request).toHaveBeenCalledTimes(12)
    poller.stop()
  })

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
