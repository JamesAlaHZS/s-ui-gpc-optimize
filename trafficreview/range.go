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
	if day := q.Get("date"); day != "" {
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
	if r.From < 0 || r.To <= r.From || r.To-r.From > 31*86400 || r.Limit < 1 || r.Limit > 500 {
		return r, errors.New("range must be positive, at most 31 days, limit 1..500")
	}
	return r, nil
}
