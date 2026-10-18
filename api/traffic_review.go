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
			obj = trafficreview.Summary{Range: r, RuleVersion: trafficreview.RuleVersion, SamplingSeconds: 10, Partial: true, Disclaimer: trafficreview.Disclaimer}
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
