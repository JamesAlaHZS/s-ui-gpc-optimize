export const trafficReviewEndpoints = [
  'summary', 'series', 'destinations', 'alerts', 'health', 'billing',
] as const

export type TrafficReviewEndpoint = typeof trafficReviewEndpoints[number]

export interface TrafficReviewQuery {
  date?: string
  timezone: 'America/Los_Angeles' | 'Asia/Shanghai' | 'UTC'
}

export interface TrafficSummary {
  from: number
  to: number
  timezone: string
  ruleVersion: string
  rxBytes: number | null
  txBytes: number | null
  proxyUploadBytes: number | null
  proxyDownloadBytes: number | null
  networkEgressBytes: number | null
  networkIngressBytes: number | null
  lastSampleAt: number | null
  samplingSeconds: number
  partial: boolean
  flowBillingConfirmed: boolean
  disclaimer: string
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
