export const trafficReviewEndpoints = [
  'summary', 'series', 'destinations', 'alerts', 'health', 'billing',
] as const

export type TrafficReviewEndpoint = typeof trafficReviewEndpoints[number]

export interface TrafficReviewQuery {
  date?: string
  startDate?: string
  endDate?: string
  timezone: 'America/Los_Angeles' | 'Asia/Shanghai' | 'UTC'
  leg?: '' | 'to_remote' | 'to_client' | 'unknown'
  layer?: 'network' | 'proxy'
  sort?: 'egress'
  limit?: number
}

export interface TrafficSKUBillingBucket {
  skuID: string
  description: string
  category: string
  handling: string
  countsTowardStandard200GiB: boolean
  usageGiB: string | null
  grossCost: string | null
  credits: string | null
  netCost: string | null
  currency: string
  rowCount: number
}

export interface TrafficStandardTierReference {
  referenceLimitGiB: string | null
  targetVMDescription: string
  targetVMEgressSKU: string
  targetVMUsedGiB: string | null
  targetVMRemainingRefGiB: string | null
  excludedTargetSKUsGiB: string | null
  quotaScope: string
  measuredScope: string
  accountWideComplete: boolean
  note: string
}

export interface TrafficBillingTotals {
  targetTotalUsageGiB: string | null
  standardFreeTierUsageGiB: string | null
  nonStandardUsageGiB: string | null
  carrierPeeringUsageGiB: string | null
  internalReferenceUsageGiB: string | null
  targetGrossCost: string | null
  targetCredits: string | null
  targetNetCost: string | null
  allImportedUsageGiB: string | null
  allImportedNetCost: string | null
  currency: string
  estimationNote: string
}

export interface TrafficBillingSyncStatus {
  enabled: boolean
  mode: string
  intervalHours: number
  lastSyncAt: number | null
  lastExportTime: number | null
  lastStatus: string
  dataPending: boolean
  lagNotice: string
  maxBytesPerQuery: number | null
  maxMonthlyBytes: number | null
  monthlyScannedBytes: number | null
  estimatedMonthlyUSD: string | null
  monthlyBudgetLimitUSD: string | null
  budgetPaused: boolean
}

export interface TrafficBillingOverview {
  targetSKUs: TrafficSKUBillingBucket[]
  totals: TrafficBillingTotals
  standardTierReference: TrafficStandardTierReference
  carrierPeeringActual: TrafficSKUBillingBucket | null
  otherSKUsCount: number
  syncStatus: TrafficBillingSyncStatus
  accountingNote: string
}

export interface TrafficCarrierCandidate {
  destination: string
  peer: string
  domain: string
  user: string
  inbound: string
  outbound: string
  protocol: string
  layer: string
  leg: 'to_remote' | 'to_client' | 'unknown'
  business: string
  egressBytes: number | null
  ingressBytes: number | null
  egressGiB: string | null
  windowSharePercent: string | null
  correlationLevel: string
  confidence: TrafficConfidence
  evidenceBasis: string
  collectionGap: string
  suggestedOutboundTag: string
  flowBillingConfirmed: boolean
}

export interface TrafficRelaySuggestion {
  status: string
  configured: boolean
  outboundTag: string
  domains: string[]
  ipCIDRs: string[]
  singBoxRule: Record<string, unknown> | null
  manualOnly: boolean
  warnings: string[]
}

export interface TrafficCarrierCorrelation {
  billedCarrierPeeringGiB: string | null
  billedCarrierPeeringCost: string | null
  billedCurrency: string
  localProxyCandidateGiB: string | null
  localNetworkCandidateGiB: string | null
  candidates: TrafficCarrierCandidate[]
  relaySuggestion: TrafficRelaySuggestion
  partial: boolean
  flowBillingConfirmed: boolean
  boundaryNote: string
}

export interface TrafficSummary {
  proxyAnalysis: TrafficProxyAnalysis | null
  from: number
  to: number
  timezone: string
  ruleVersion: string
  rxBytes: number | null
  txBytes: number | null
  nicTotalBytes: number | null
  proxyUploadBytes: number | null
  proxyDownloadBytes: number | null
  proxyTotalBytes: number | null
  networkEgressBytes: number | null
  networkIngressBytes: number | null
  networkTotalBytes: number | null
  toRemoteEgressBytes: number | null
  toClientEgressBytes: number | null
  unknownDirectionEgressBytes: number | null
  egressDifferenceBytes: number | null
  networkSampleCount: number | null
  proxySampleCount: number | null
  firstSampleAt: number | null
  lastSampleAt: number | null
  samplingSeconds: number
  partial: boolean
  destinationDetailsPartial: boolean
  flowBillingConfirmed: boolean
  disclaimer: string
  billingOverview: TrafficBillingOverview | null
  carrierCorrelation: TrafficCarrierCorrelation | null
}

export interface TrafficProxyBillingScope {
  projectID: string
  currency: string
  skuIDs: string[]
  status: string
  reason: string
  pairCount: number | null
  eligibleDays: number | null
  candidateCorrelation: number | null
  baselineCorrelation: number | null
  confoundingCorrelation: number | null
  carrierUsageGiB: string | null
  carrierNetCost: string | null
  billingDayCount: number | null
  evidenceDayCount: number | null
  zeroUsageDays: number | null
}

export interface TrafficProxyAnalysis {
  remoteUploadBytes: number | null
  remoteDownloadBytes: number | null
  forwardedEgressBytes: number | null
  forwardedIngressBytes: number | null
  candidateUploadBytes: number | null
  candidateDownloadBytes: number | null
  candidateForwardedEgressBytes: number | null
  candidateForwardedIngressBytes: number | null
  otherForwardedEgressBytes: number | null
  unknownDomainBytes: number | null
  observations: number | null
  hasObservations: boolean | null
  firstSampleAt: number | null
  lastSampleAt: number | null
  partial: boolean
  measurementBasis: string
  flowBillingConfirmed: boolean
  billingComparison: {
    analysisFrom: number | null
    analysisTo: number | null
    status: string
    reason: string
    pairCount: number | null
    eligibleDays: number | null
    minMatchedDays: number | null
    excludedRecentDays: number | null
    timezone: string
    scope: string
    flowBillingConfirmed: boolean
    scopes: TrafficProxyBillingScope[]
  }
}

export interface TrafficSeriesPoint {
  time: number
  rxBytes: number | null
  txBytes: number | null
  rxBps: number | null
  txBps: number | null
  quality: string
}

export type TrafficConfidence = 'observed' | 'inferred' | 'unknown'
export type FeeStatus = 'unknown' | 'carrier_candidate' | 'standard_candidate'

export interface TrafficDestination {
  layer: 'proxy' | 'network' | 'unknown'
  peer: string
  domain: string
  user: string
  inbound: string
  outbound: string
  protocol: string
  business: string
  confidence: TrafficConfidence
  upBytes: number | null
  downBytes: number | null
  txBytes: number | null
  rxBytes: number | null
  leg: 'to_remote' | 'to_client' | 'unknown'
  feeStatus: FeeStatus
  feeBasis: string
  ruleVersion: string
}

export interface TrafficAlert {
  id: string
  time: number
  kind: string
  message: string
  ruleVersion: string
}

export interface TrafficHealth {
  enabled: boolean
  available: boolean
  lastSampleAt: number | null
  samplingSeconds: number | null
  networkInterface: string
  databaseBytes: number | null
  status: string
  conntrack: {
    available: boolean
    degraded: boolean
    lastError: string
    eventGaps: number | null
    eventRestarts: number | null
    parseErrors: number | null
  }
  gaps: number | null
}

export interface TrafficBillingRecord {
  date: string
  projectID: string
  skuID: string
  description: string
  usageGiB: string | null
  cost: string | null
  currency: string
  sourceSHA256: string
  scope: 'project_day_sku' | 'unknown'
  category?: string
  accountingTreatment?: string
  countsTowardStandard200GiB?: boolean
  invoiceMonth?: string
  credits?: string | null
  netCost?: string | null
  exportTime?: number | null
  syncedAt?: number | null
  syncSource?: string
}

export interface TrafficReviewResponse<T = unknown> {
  success: boolean
  msg: string
  obj: T
}

export interface TrafficReviewState {
  data: unknown
  updatedAt: number | null
  error: string | null
}

export type TrafficReviewStates = Record<TrafficReviewEndpoint, TrafficReviewState>

export function emptyTrafficReviewStates(): TrafficReviewStates {
  return Object.fromEntries(trafficReviewEndpoints.map((endpoint) => [endpoint, {
    data: null,
    updatedAt: null,
    error: null,
  }])) as TrafficReviewStates
}
