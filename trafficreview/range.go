package trafficreview

import (
	"errors"
	"net/url"
	"strconv"
	"time"
	_ "time/tzdata"
)

func ParseRange(q url.Values, now time.Time) (Range, error) {
	r := DefaultRange(now)
	for _, key := range []string{"date", "startDate", "endDate", "from", "to", "timezone", "limit", "layer", "leg", "sort"} {
		if values, ok := q[key]; ok && (len(values) != 1 || values[0] == "" && key != "layer" && key != "leg" && key != "sort" && key != "timezone") {
			return r, errors.New("query parameters must have one non-empty value")
		}
	}
	if v := q.Get("timezone"); v != "" {
		r.Timezone = v
	}
	switch r.Timezone {
	case "UTC", "America/Los_Angeles", "Asia/Shanghai":
	default:
		return r, errors.New("unsupported timezone")
	}
	loc, e := time.LoadLocation(r.Timezone)
	if e != nil {
		return r, e
	}
	if q.Has("startDate") || q.Has("endDate") {
		if !q.Has("startDate") || !q.Has("endDate") || q.Has("date") || q.Has("from") || q.Has("to") {
			return r, errors.New("startDate and endDate are required together and cannot be combined with date/from/to")
		}
		start, err := time.ParseInLocation("2006-01-02", q.Get("startDate"), loc)
		if err != nil {
			return r, err
		}
		end, err := time.ParseInLocation("2006-01-02", q.Get("endDate"), loc)
		if err != nil {
			return r, err
		}
		r.From, r.To = start.Unix(), end.AddDate(0, 0, 1).Unix()
	} else if day := q.Get("date"); day != "" {
		if q.Get("from") != "" || q.Get("to") != "" {
			return r, errors.New("date cannot be combined with from/to")
		}
		d, e := time.ParseInLocation("2006-01-02", day, loc)
		if e != nil {
			return r, e
		}
		r.From = d.Unix()
		r.To = d.AddDate(0, 0, 1).Unix()
	} else {
		if v := q.Get("from"); v != "" {
			r.From, e = strconv.ParseInt(v, 10, 64)
			if e != nil {
				return r, e
			}
		}
		if v := q.Get("to"); v != "" {
			r.To, e = strconv.ParseInt(v, 10, 64)
			if e != nil {
				return r, e
			}
		}
	}
	if v := q.Get("limit"); v != "" {
		r.Limit, e = strconv.Atoi(v)
		if e != nil {
			return r, e
		}
	}
	r.Layer, r.Leg, r.Sort = q.Get("layer"), q.Get("leg"), q.Get("sort")
	if r.Layer != "" && r.Layer != "network" && r.Layer != "proxy" {
		return r, errors.New("layer must be network or proxy")
	}
	if r.Leg != "" && r.Leg != "to_remote" && r.Leg != "to_client" && r.Leg != "unknown" {
		return r, errors.New("leg must be to_remote, to_client or unknown")
	}
	if r.Sort != "" && r.Sort != "egress" && r.Sort != "total" {
		return r, errors.New("sort must be egress or total")
	}
	// Use civil years in the requested timezone, including leap days and DST.
	// The end is exclusive, so Jan 1 through Dec 31 of three years is valid.
	if r.From < 0 || r.To <= r.From || r.To > 253402300800 || r.To > time.Unix(r.From, 0).In(loc).AddDate(3, 0, 0).Unix() || r.Limit < 1 || r.Limit > 500 {
		return r, errors.New("range must be positive, at most 3 calendar years, limit 1..500")
	}
	return r, nil
}
