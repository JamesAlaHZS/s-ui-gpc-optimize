import type {
  FeeStatus, TrafficAlert, TrafficBillingOverview, TrafficBillingRecord,
  TrafficCarrierCandidate, TrafficCarrierCorrelation, TrafficConfidence,
  TrafficDestination, TrafficHealth, TrafficReviewEndpoint, TrafficSKUBillingBucket,
  TrafficSeriesPoint, TrafficSummary,
} from '@/types/trafficReview'

type RecordValue = Record<string, unknown>
const record = (value: unknown): RecordValue | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : null
const text = (value: unknown): string => typeof value === 'string' ? value : ''
const number = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null
const decimal = (value: unknown): string | null =>
  typeof value === 'string' && /^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(value) ? value : null
const boolean = (value: unknown): boolean => value === true
const stringArray = (value: unknown): string[] =>
  Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []

function list(value: unknown): RecordValue[] | null {
  if (!Array.isArray(value)) return null
  return value.map(record).filter((item): item is RecordValue => item !== null)
}

function adaptSKUBucket(value: unknown): TrafficSKUBillingBucket | null {
  const item = record(value)
  if (!item) return null
  return {
    skuID: text(item.skuID),
    description: text(item.description),
    category: text(item.category),
    handling: text(item.handling),
    countsTowardStandard200GiB: boolean(item.countsTowardStandard200GiB),
    usageGiB: decimal(item.usageGiB),
    grossCost: decimal(item.grossCost),
    credits: decimal(item.credits),
    netCost: decimal(item.netCost),
    currency: text(item.currency),
    rowCount: number(item.rowCount) ?? 0,
  }
}

function adaptBillingOverview(value: unknown): TrafficBillingOverview | null {
  const item = record(value)
  if (!item) return null
  const ref = record(item.standardTierReference) ?? {}
  const tot = record(item.totals) ?? {}
  const sync = record(item.syncStatus) ?? {}
  const targetSKUs = (list(item.targetSKUs) ?? [])
    .map(adaptSKUBucket)
    .filter((b): b is TrafficSKUBillingBucket => b !== null)
  return {
    targetSKUs,
    totals: {
      targetTotalUsageGiB: decimal(tot.targetTotalUsageGiB),
      standardFreeTierUsageGiB: decimal(tot.standardFreeTierUsageGiB),
      nonStandardUsageGiB: decimal(tot.nonStandardUsageGiB),
      carrierPeeringUsageGiB: decimal(tot.carrierPeeringUsageGiB),
      internalReferenceUsageGiB: decimal(tot.internalReferenceUsageGiB),
      targetGrossCost: decimal(tot.targetGrossCost),
      targetCredits: decimal(tot.targetCredits),
      targetNetCost: decimal(tot.targetNetCost),
      allImportedUsageGiB: decimal(tot.allImportedUsageGiB),
      allImportedNetCost: decimal(tot.allImportedNetCost),
      currency: text(tot.currency),
      estimationNote: text(tot.estimationNote),
    },
    standardTierReference: {
      referenceLimitGiB: decimal(ref.referenceLimitGiB),
      targetVMDescription: text(ref.targetVMDescription),
      targetVMEgressSKU: text(ref.targetVMEgressSKU),
      targetVMUsedGiB: decimal(ref.targetVMUsedGiB),
      targetVMRemainingRefGiB: decimal(ref.targetVMRemainingRefGiB),
      excludedTargetSKUsGiB: decimal(ref.excludedTargetSKUsGiB),
      quotaScope: text(ref.quotaScope),
      measuredScope: text(ref.measuredScope),
      accountWideComplete: boolean(ref.accountWideComplete),
      note: text(ref.note),
    },
    carrierPeeringActual: adaptSKUBucket(item.carrierPeeringActual),
    otherSKUsCount: number(item.otherSKUsCount) ?? 0,
    syncStatus: {
      enabled: boolean(sync.enabled),
      mode: text(sync.mode),
      intervalHours: number(sync.intervalHours) ?? 12,
      lastSyncAt: number(sync.lastSyncAt),
      lastExportTime: number(sync.lastExportTime),
      lastStatus: text(sync.lastStatus),
      dataPending: boolean(sync.dataPending),
      lagNotice: text(sync.lagNotice),
      maxBytesPerQuery: number(sync.maxBytesPerQuery),
      maxMonthlyBytes: number(sync.maxMonthlyBytes),
      monthlyScannedBytes: number(sync.monthlyScannedBytes),
      estimatedMonthlyUSD: decimal(sync.estimatedMonthlyUSD),
      monthlyBudgetLimitUSD: decimal(sync.monthlyBudgetLimitUSD),
      budgetPaused: boolean(sync.budgetPaused),
    },
    accountingNote: text(item.accountingNote),
  }
}

function adaptCarrierCorrelation(value: unknown): TrafficCarrierCorrelation | null {
  const item = record(value)
  if (!item) return null
  const relay = record(item.relaySuggestion) ?? {}
  const candidates = (list(item.candidates) ?? []).map((c): TrafficCarrierCandidate => ({
    destination: text(c.destination),
    peer: text(c.peer),
    domain: text(c.domain),
    user: text(c.user),
    inbound: text(c.inbound),
    outbound: text(c.outbound),
    protocol: text(c.protocol),
    layer: text(c.layer),
    leg: c.leg === 'to_remote' || c.leg === 'to_client' ? c.leg : 'unknown',
    business: text(c.business),
    egressBytes: number(c.egressBytes),
    ingressBytes: number(c.ingressBytes),
    egressGiB: decimal(c.egressGiB),
    windowSharePercent: decimal(c.windowSharePercent),
    correlationLevel: text(c.correlationLevel),
    confidence: (['observed', 'inferred'].includes(text(c.confidence)) ? c.confidence : 'unknown') as TrafficConfidence,
    evidenceBasis: text(c.evidenceBasis),
    collectionGap: text(c.collectionGap),
    suggestedOutboundTag: text(c.suggestedOutboundTag),
    flowBillingConfirmed: false,
  }))
  return {
    billedCarrierPeeringGiB: decimal(item.billedCarrierPeeringGiB),
    billedCarrierPeeringCost: decimal(item.billedCarrierPeeringCost),
    billedCurrency: text(item.billedCurrency),
    localProxyCandidateGiB: decimal(item.localProxyCandidateGiB),
    localNetworkCandidateGiB: decimal(item.localNetworkCandidateGiB),
    candidates,
    relaySuggestion: {
      status: text(relay.status) || 'pending_configuration',
      configured: boolean(relay.configured),
      outboundTag: text(relay.outboundTag) || 'pending_configuration',
      domains: stringArray(relay.domains),
      ipCIDRs: stringArray(relay.ipCIDRs),
      singBoxRule: record(relay.singBoxRule),
      manualOnly: relay.manualOnly !== false,
      warnings: stringArray(relay.warnings),
    },
    partial: boolean(item.partial),
    flowBillingConfirmed: false,
    boundaryNote: text(item.boundaryNote),
  }
}

export function adaptTrafficReviewData(endpoint: TrafficReviewEndpoint, value: unknown): unknown {
  if (endpoint === 'summary') {
    const item = record(value)
    if (!item) return null
    const result: TrafficSummary = {
      from: number(item.from) ?? 0,
      to: number(item.to) ?? 0,
      timezone: text(item.timezone),
      ruleVersion: text(item.ruleVersion),
      rxBytes: number(item.rxBytes),
      txBytes: number(item.txBytes),
      nicTotalBytes: number(item.nicTotalBytes),
      proxyUploadBytes: number(item.proxyUploadBytes),
      proxyDownloadBytes: number(item.proxyDownloadBytes),
      proxyTotalBytes: number(item.proxyTotalBytes),
      networkEgressBytes: number(item.networkEgressBytes),
      networkIngressBytes: number(item.networkIngressBytes),
      networkTotalBytes: number(item.networkTotalBytes),
      toRemoteEgressBytes: number(item.toRemoteEgressBytes),
      toClientEgressBytes: number(item.toClientEgressBytes),
      unknownDirectionEgressBytes: number(item.unknownDirectionEgressBytes),
      egressDifferenceBytes: number(item.egressDifferenceBytes),
      networkSampleCount: number(item.networkSampleCount),
      proxySampleCount: number(item.proxySampleCount),
      firstSampleAt: number(item.firstSampleAt),
      lastSampleAt: number(item.lastSampleAt),
      samplingSeconds: number(item.samplingSeconds) ?? 0,
      partial: boolean(item.partial),
      destinationDetailsPartial: boolean(item.destinationDetailsPartial),
      // A flow-level billing claim is never inferred from counters.
      flowBillingConfirmed: boolean(item.flowBillingConfirmed),
      disclaimer: text(item.disclaimer),
      billingOverview: adaptBillingOverview(item.billingOverview),
      carrierCorrelation: adaptCarrierCorrelation(item.carrierCorrelation),
    }
    return result
  }
  if (endpoint === 'series') {
    return list(value)?.map((item): TrafficSeriesPoint => ({
      time: number(item.time) ?? 0,
      rxBytes: number(item.rxBytes), txBytes: number(item.txBytes),
      rxBps: number(item.rxBps), txBps: number(item.txBps), quality: text(item.quality),
    })) ?? null
  }
  if (endpoint === 'destinations') {
    return list(value)?.map((item): TrafficDestination => ({
      layer: item.layer === 'proxy' || item.layer === 'network' ? item.layer : 'unknown',
      peer: text(item.peer), domain: text(item.domain), user: text(item.user),
      inbound: text(item.inbound), outbound: text(item.outbound), protocol: text(item.protocol),
      business: text(item.business),
      confidence: (['observed', 'inferred'].includes(text(item.confidence)) ? item.confidence : 'unknown') as TrafficConfidence,
      upBytes: number(item.upBytes), downBytes: number(item.downBytes),
      txBytes: number(item.txBytes), rxBytes: number(item.rxBytes),
      leg: item.leg === 'to_remote' || item.leg === 'to_client' ? item.leg : 'unknown',
      feeStatus: (['carrier_candidate', 'standard_candidate'].includes(text(item.feeStatus)) ? item.feeStatus : 'unknown') as FeeStatus,
      feeBasis: text(item.feeBasis),
      ruleVersion: text(item.ruleVersion),
    })) ?? null
  }
  if (endpoint === 'alerts') {
    return list(value)?.map((item): TrafficAlert => ({
      id: typeof item.id === 'string' || typeof item.id === 'number' && Number.isFinite(item.id) ? String(item.id) : '',
      time: number(item.time) ?? 0, kind: text(item.kind),
      message: text(item.message), ruleVersion: text(item.ruleVersion),
    })) ?? null
  }
  if (endpoint === 'health') {
    const item = record(value)
    if (!item) return null
    const conntrack = record(item.conntrack) ?? {}
    const result: TrafficHealth = {
      enabled: boolean(item.enabled), available: boolean(item.available),
      lastSampleAt: number(item.lastSampleAt), samplingSeconds: number(item.samplingSeconds),
      networkInterface: text(item.networkInterface), databaseBytes: number(item.databaseBytes),
      status: text(item.status),
      conntrack: {
        available: boolean(conntrack.available), degraded: boolean(conntrack.degraded),
        lastError: text(conntrack.lastError), eventGaps: number(conntrack.eventGaps),
        eventRestarts: number(conntrack.eventRestarts), parseErrors: number(conntrack.parseErrors),
      },
      gaps: number(item.gaps),
    }
    return result
  }
  const rows = list(value)
  return rows?.map((item): TrafficBillingRecord => ({
    date: text(item.date), projectID: text(item.projectID), skuID: text(item.skuID),
    description: text(item.description), usageGiB: decimal(item.usageGiB),
    cost: decimal(item.cost), currency: text(item.currency),
    sourceSHA256: text(item.sourceSHA256),
    scope: item.scope === 'project_day_sku' ? 'project_day_sku' : 'unknown',
    category: text(item.category),
    accountingTreatment: text(item.accountingTreatment),
    countsTowardStandard200GiB: boolean(item.countsTowardStandard200GiB),
    invoiceMonth: text(item.invoiceMonth),
    credits: decimal(item.credits),
    netCost: decimal(item.netCost),
    exportTime: number(item.exportTime),
    syncedAt: number(item.syncedAt),
    syncSource: text(item.syncSource),
  })) ?? null
}
