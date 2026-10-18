import type { TrafficBillingOverview, TrafficDestination } from '@/types/trafficReview'

export function outboundPeers(rows: TrafficDestination[] | null): TrafficDestination[] {
  return (rows ?? []).filter(row => row.layer === 'network' && row.txBytes != null && row.txBytes > 0)
    .sort((a, b) => (b.txBytes ?? 0) - (a.txBytes ?? 0))
}

export function proxyPeerTotal(row: TrafficDestination): number | null {
  return row.upBytes == null || row.downBytes == null ? null : row.upBytes + row.downBytes
}

export function proxyPeers(rows: TrafficDestination[] | null): TrafficDestination[] {
  return (rows ?? []).filter(row => row.layer === 'proxy')
    .sort((a, b) => (proxyPeerTotal(b) ?? -1) - (proxyPeerTotal(a) ?? -1))
}

export function billingStatusKey(overview: TrafficBillingOverview | null): string {
  const status = overview?.syncStatus
  if (!status) return 'syncNever'
  if (status.mode === 'offline_snapshot') return 'syncOffline'
  if (status.budgetPaused) return 'syncBudget'
  if (['error', 'reserved', 'rejected_budget'].includes(status.lastStatus)) return 'syncError'
  if (status.lastStatus === 'dry_run_ok') return 'syncDry'
  if (!status.lastSyncAt) return 'syncNever'
  return status.dataPending ? 'syncPending' : 'syncReady'
}

export function connectionErrorKey(message: string): string {
  if (/nf_conntrack_acct|byte counters are not available/i.test(message)) return 'conntrackAccounting'
  if (/unavailable|not found|no such file/i.test(message)) return 'conntrackMissing'
  if (/permission|not permitted/i.test(message)) return 'conntrackPermission'
  return 'conntrackError'
}

export function relayRule(tagInput: string, domainInput: string): Record<string, unknown> | null {
  const tag = tagInput.trim()
  const domains = [...new Set(domainInput.toLowerCase().split(/[\s,]+/).filter(Boolean))]
  const validDomain = /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/
  if (!tag || tag.length > 128 || /[\r\n]/.test(tag) || !domains.length || domains.length > 100 ||
    domains.some(domain => !validDomain.test(domain) || /^\d+(?:\.\d+){3}$/.test(domain))) return null
  return { domain_suffix: domains, action: 'route', outbound: tag }
}
