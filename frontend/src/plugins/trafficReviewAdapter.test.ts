import { describe, expect, it } from 'vitest'
import { adaptTrafficReviewData } from './trafficReviewAdapter'
import type { TrafficAlert, TrafficBillingRecord, TrafficDestination, TrafficSummary } from '@/types/trafficReview'

describe('traffic review response adapter', () => {
  it('preserves unavailable mixed-currency totals instead of presenting zero', () => {
    const summary = adaptTrafficReviewData('summary', {
      billingOverview: {
        totals: { currency: 'MIXED', targetTotalUsageGiB: '5', targetNetCost: '' },
        targetSKUs: [{ currency: 'MIXED', netCost: '', grossCost: '', usageGiB: '5' }],
      },
    }) as TrafficSummary
    expect(summary.billingOverview?.totals.currency).toBe('MIXED')
    expect(summary.billingOverview?.totals.targetNetCost).toBeNull()
    expect(summary.billingOverview?.targetSKUs[0].netCost).toBeNull()
    expect(summary.billingOverview?.totals.targetTotalUsageGiB).toBe('5')
  })
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

  it('adapts 5-SKU billing overview, standard tier reference, and carrier correlation without confirming flow billing', () => {
    const summary = adaptTrafficReviewData('summary', {
      ruleVersion: 'local-v1',
      flowBillingConfirmed: true,
      billingOverview: {
        targetSKUs: [
          { skuID: '4986-FDE4-A69A', description: 'Network Standard Data Transfer Out to Internet from Oregon', category: 'standard_internet_egress', handling: 'target_vm_standard_tier', countsTowardStandard200GiB: true, usageGiB: '69.03', grossCost: '0', credits: '0', netCost: '0', currency: 'USD', rowCount: 1 },
          { skuID: '984A-1F27-2D1F', description: 'Network Data Transfer Out via Carrier Peering Network - Americas Based', category: 'carrier_peering_egress', handling: 'billed_carrier_peering', countsTowardStandard200GiB: false, usageGiB: '10.3', grossCost: '0.82', credits: '0', netCost: '0.82', currency: 'USD', rowCount: 1 },
        ],
        totals: {
          targetTotalUsageGiB: '79.35',
          standardFreeTierUsageGiB: '69.03',
          nonStandardUsageGiB: '10.32',
          carrierPeeringUsageGiB: '10.3',
          internalReferenceUsageGiB: '0.02',
          targetGrossCost: '0.82',
          targetCredits: '0',
          targetNetCost: '0.82',
          allImportedUsageGiB: '79.35',
          allImportedNetCost: '0.82',
          currency: 'USD',
        },
        standardTierReference: {
          referenceLimitGiB: '200',
          targetVMDescription: 'Network Standard Data Transfer Out to Internet from Oregon',
          targetVMEgressSKU: '4986-FDE4-A69A',
          targetVMUsedGiB: '69.03',
          targetVMRemainingRefGiB: '130.97',
          excludedTargetSKUsGiB: '10.32',
          quotaScope: 'billing_account_wide',
          measuredScope: 'target_oregon_vm_sku_only',
          accountWideComplete: false,
        },
        syncStatus: {
          enabled: true,
          mode: 'standard_export_12h',
          intervalHours: 12,
          dataPending: false,
          maxBytesPerQuery: 67108864,
          maxMonthlyBytes: 4160749568,
          estimatedMonthlyUSD: '0.023651',
          monthlyBudgetLimitUSD: '0.05',
          budgetPaused: false,
        },
      },
      carrierCorrelation: {
        billedCarrierPeeringGiB: '10.3',
        billedCarrierPeeringCost: '0.82',
        billedCurrency: 'USD',
        localProxyCandidateGiB: '4.0',
        localNetworkCandidateGiB: '4.0',
        flowBillingConfirmed: true,
        candidates: [
          { destination: 'video.example.com', domain: 'video.example.com', user: 'alice', outbound: 'direct', layer: 'proxy', leg: 'to_remote', egressGiB: '4', windowSharePercent: '80', correlationLevel: 'window_correlated', confidence: 'inferred', flowBillingConfirmed: true },
        ],
        relaySuggestion: {
          status: 'pending_configuration',
          configured: false,
          outboundTag: 'pending_configuration',
          domains: ['video.example.com'],
          ipCIDRs: [],
          manualOnly: true,
        },
      },
    }) as TrafficSummary

    expect(summary.billingOverview?.totals.targetTotalUsageGiB).toBe('79.35')
    expect(summary.billingOverview?.totals.targetNetCost).toBe('0.82')
    expect(summary.billingOverview?.standardTierReference.targetVMUsedGiB).toBe('69.03')
    expect(summary.billingOverview?.standardTierReference.excludedTargetSKUsGiB).toBe('10.32')
    expect(summary.billingOverview?.standardTierReference.accountWideComplete).toBe(false)
    expect(summary.carrierCorrelation?.flowBillingConfirmed).toBe(false)
    expect(summary.carrierCorrelation?.candidates[0].flowBillingConfirmed).toBe(false)
    expect(summary.carrierCorrelation?.relaySuggestion.status).toBe('pending_configuration')
    expect(summary.carrierCorrelation?.relaySuggestion.configured).toBe(false)
    expect(summary.carrierCorrelation?.relaySuggestion.outboundTag).toBe('pending_configuration')
  })
})
