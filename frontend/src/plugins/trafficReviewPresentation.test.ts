import { describe, expect, it } from 'vitest'
import { billingStatusKey, connectionErrorKey, outboundPeers, proxyPeers, proxyPeerTotal, relayRule } from './trafficReviewPresentation'
import { adaptTrafficReviewData } from './trafficReviewAdapter'
import type { TrafficBillingOverview, TrafficDestination, TrafficSummary } from '@/types/trafficReview'
import { trafficReviewEn, trafficReviewZh, trafficReviewZhHant } from '@/locales/trafficReview'

describe('outbound presentation', () => {
  it('ranks proxy sessions by upload plus download and retains unknown domains', () => {
    const rows = [
      { layer: 'proxy', domain: 'chatgpt.com', upBytes: 100, downBytes: 1 },
      { layer: 'network', domain: '', upBytes: 9999, downBytes: 9999 },
      { layer: 'proxy', domain: '', upBytes: 1, downBytes: 200 },
    ] as TrafficDestination[]
    expect(proxyPeers(rows).map(row => row.domain)).toEqual(['', 'chatgpt.com'])
    expect(proxyPeerTotal(rows[2]!)).toBe(201)
    expect(proxyPeerTotal({ upBytes: null, downBytes: 2 } as TrafficDestination)).toBeNull()
  })
  it('ranks network TX without adding proxy payload or RX', () => {
    const rows = [
      { layer: 'network', peer: 'incoming', txBytes: 2, rxBytes: 999999 },
      { layer: 'proxy', peer: 'payload', txBytes: 999999 },
      { layer: 'network', peer: 'client', txBytes: 100 },
      { layer: 'network', peer: 'missing', txBytes: null },
    ] as TrafficDestination[]
    expect(outboundPeers(rows).map(row => row.peer)).toEqual(['client', 'incoming'])
    expect(rows[0]!.peer).toBe('incoming')
    const summary = adaptTrafficReviewData('summary', { txBytes: 10, rxBytes: 50, toRemoteEgressBytes: 3, toClientEgressBytes: 7 }) as TrafficSummary
    expect(summary.txBytes).toBe(10)
    expect(summary.toClientEgressBytes).toBe(7)
    expect((adaptTrafficReviewData('summary', {}) as TrafficSummary).toRemoteEgressBytes).toBeNull()
  })
  it('does not describe missing, failed or offline bills as live charges', () => {
    const bill = (status: object) => ({ syncStatus: status }) as TrafficBillingOverview
    expect(billingStatusKey(null)).toBe('syncNever')
    expect(billingStatusKey(bill({ mode: 'offline_snapshot', lastSyncAt: 1 }))).toBe('syncOffline')
    expect(billingStatusKey(bill({ lastStatus: 'error', lastSyncAt: 1 }))).toBe('syncError')
    expect(billingStatusKey(bill({ budgetPaused: true }))).toBe('syncBudget')
    expect(billingStatusKey(bill({ lastStatus: 'dry_run_ok' }))).toBe('syncDry')
    expect(billingStatusKey(bill({ lastSyncAt: 1, dataPending: true }))).toBe('syncPending')
    expect(billingStatusKey(bill({ lastSyncAt: 1, dataPending: false }))).toBe('syncReady')
  })
  it('validates and deduplicates only domain suffix rules for an existing tag', () => {
    expect(relayRule(' relay-out ', 'Example.COM\nexample.com, www.example.org')).toEqual({ domain_suffix: ['example.com', 'www.example.org'], action: 'route', outbound: 'relay-out' })
    for (const value of ['', 'https://example.com', '*.example.com', '127.0.0.1', 'a..com', '-bad.com']) expect(relayRule('relay-out', value)).toBeNull()
    expect(relayRule('bad\ntag', 'example.com')).toBeNull()
  })
  it('provides Chinese for every traffic review key, including the full accounting boundary', () => {
    function leaves(value: object, prefix = ''): string[] {
      return Object.entries(value).flatMap(([key, item]) => typeof item === 'object' ? leaves(item, `${prefix}${key}.`) : [`${prefix}${key}`])
    }
    expect(leaves(trafficReviewZh)).toEqual(leaves(trafficReviewEn))
    expect(leaves(trafficReviewZhHant)).toEqual(leaves(trafficReviewEn))
    expect(trafficReviewZh.accountingDetail).not.toContain('Local byte')
    expect(trafficReviewZh.accountingDetail).toContain('不能相加')
    expect(connectionErrorKey('permission denied')).toBe('conntrackPermission')
    expect(connectionErrorKey('nf_conntrack_acct=0')).toBe('conntrackAccounting')
  })
})
