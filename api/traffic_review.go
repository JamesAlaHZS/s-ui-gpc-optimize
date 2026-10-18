package api

import (
	"net/http"
	"time"

	"github.com/alireza0/s-ui/service"
	"github.com/alireza0/s-ui/trafficreview"
	"github.com/gin-gonic/gin"
)

func (a *APIHandler) trafficReview(c *gin.Context) {
	// Explicitly authenticate every action, in addition to the normal cookie
	// group. No apiv2 token is accepted by this read-only surface.
	if !IsLogin(c) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, Msg{Success: false, Msg: "Invalid login"})
		return
	}
	r, err := trafficreview.ParseRange(c.Request.URL.Query(), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, Msg{Success: false, Msg: err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	s := service.TrafficReviewStore()
	var obj any
	if s == nil {
		switch c.Param("reviewAction") {
		case "health":
			obj = trafficreview.Health{Status: "disabled", SamplingSeconds: 10, Conntrack: map[string]any{"available": false, "degraded": true, "lastError": "Local collector not enabled"}}
		case "summary":
			overview := trafficreview.SummarizeBills(nil, trafficreview.BillingSyncStatus{
				Mode:                  "manual_csv_fallback",
				IntervalHours:         trafficreview.DefaultSyncIntervalHours,
				DataPending:           true,
				LagNotice:             trafficreview.BillingLagNotice,
				MaxBytesPerQuery:      trafficreview.DefaultMaxBytesPerQuery,
				MaxMonthlyBytes:       trafficreview.DefaultMaxMonthlyBytes,
				EstimatedMonthlyUSD:   "0",
				MonthlyBudgetLimitUSD: "0.05",
			})
			obj = trafficreview.Summary{Range: r, RuleVersion: trafficreview.RuleVersion, SamplingSeconds: 10, Partial: true, Disclaimer: trafficreview.Disclaimer, BillingOverview: &overview}
		case "billing-overview":
			obj = trafficreview.SummarizeBills(nil, trafficreview.BillingSyncStatus{
				Mode:                  "manual_csv_fallback",
				IntervalHours:         trafficreview.DefaultSyncIntervalHours,
				DataPending:           true,
				LagNotice:             trafficreview.BillingLagNotice,
				MaxBytesPerQuery:      trafficreview.DefaultMaxBytesPerQuery,
				MaxMonthlyBytes:       trafficreview.DefaultMaxMonthlyBytes,
				EstimatedMonthlyUSD:   "0",
				MonthlyBudgetLimitUSD: "0.05",
			})
		case "candidates":
			obj = trafficreview.CarrierCorrelationReport{Range: r, Candidates: []trafficreview.CarrierPeeringCandidate{}, RelaySuggestion: trafficreview.RelayRouteSuggestion{Status: "pending_configuration", Configured: false, OutboundTag: trafficreview.DefaultRelayOutboundTag, Domains: []string{}, IPCIDRs: []string{}, ManualOnly: true}, Partial: true, FlowBillingConfirmed: false, BoundaryNote: trafficreview.CarrierBoundaryNote}
		case "series", "destinations", "alerts", "billing":
			obj = []any{}
		default:
			c.Status(http.StatusNotFound)
			return
		}
	} else {
		switch c.Param("reviewAction") {
		case "summary":
			obj, err = s.Summary(r)
		case "series":
			obj, err = s.Series(r)
		case "destinations":
			obj, err = s.Destinations(r)
		case "alerts":
			obj, err = s.Alerts(r)
		case "health":
			obj, err = s.Health(time.Now().Unix())
		case "billing":
			obj, err = s.Bills(r)
		case "billing-overview":
			obj, err = s.BillingOverview(r, time.Now())
		case "candidates":
			obj, err = s.CarrierCorrelation(r)
		default:
			c.Status(http.StatusNotFound)
			return
		}
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, Msg{Success: false, Msg: "Local traffic review temporarily unavailable"})
		return
	}
	jsonObj(c, obj, nil)
}
