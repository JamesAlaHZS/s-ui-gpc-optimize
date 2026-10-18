import { describe, expect, it } from 'vitest'
import { adaptTrafficReviewData } from './trafficReviewAdapter'
import type { TrafficAlert, TrafficBillingRecord, TrafficDestination, TrafficSummary } from '@/types/trafficReview'

describe('traffic review response adapter', () => {
  it('keeps missing measurements unknown instead of reporting zero or free', () => {
    const summary = adaptTrafficReviewData('summary', { ruleVersion: '2026-10' }) as TrafficSummary
    expect(summary.proxyUploadBytes).toBeNull()
    expect(summary.networkEgressBytes).toBeNull()
    expect(summary.flowBillingConfirmed).toBe(false)

    const rows = adaptTrafficReviewData('destinations', [{ peer: 'example.net', outbound: 'direct' }]) as TrafficDestination[]
    expect(rows[0].feeStatus).toBe('unknown')
    expect(rows[0].confidence).toBe('unknown')
    expect(rows[0].txBytes).toBeNull()
  })

  it('does not upgrade an unrecognized billing scope to confirmation', () => {
    const records = adaptTrafficReviewData('billing', [{ date: '2026-10-08', scope: 'connection', cost: '0.00000001', usageGiB: '3.820000000000000001' }]) as TrafficBillingRecord[]
    expect(records[0].scope).toBe('unknown')
    expect(records[0].cost).toBe('0.00000001')
    expect(records[0].usageGiB).toBe('3.820000000000000001')
    expect(adaptTrafficReviewData('billing', { cost: 1 })).toBeNull()
    const invalid = adaptTrafficReviewData('billing', [{ cost: 0.31, usageGiB: 'NaN' }]) as TrafficBillingRecord[]
    expect(invalid[0].cost).toBeNull()
    expect(invalid[0].usageGiB).toBeNull()
  })

  it('preserves numeric alert IDs as strings and destination rule versions', () => {
    const alerts = adaptTrafficReviewData('alerts', [{ id: 42, time: 1 }]) as TrafficAlert[]
    expect(alerts[0].id).toBe('42')
    const rows = adaptTrafficReviewData('destinations', [{ ruleVersion: 'operator-v1', feeStatus: 'carrier_candidate' }]) as TrafficDestination[]
    expect(rows[0].ruleVersion).toBe('operator-v1')
    expect(rows[0].feeStatus).toBe('carrier_candidate')
  })
})
