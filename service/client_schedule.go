package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/alireza0/s-ui/database/model"
	"gorm.io/gorm"
)

func nextMonthlyReset(now time.Time, clock, zone string) (time.Time, error) {
	wall, err := time.Parse("15:04", clock)
	if err != nil || wall.Format("15:04") != clock {
		return time.Time{}, fmt.Errorf("monthly reset time must use HH:MM (24-hour clock)")
	}
	if zone == "" || zone == "Local" {
		return time.Time{}, fmt.Errorf("monthly reset requires a named time zone such as Asia/Shanghai")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid monthly reset time zone %q: %w", zone, err)
	}
	year, month, _ := now.In(loc).Date()
	next := time.Date(year, month, 1, wall.Hour(), wall.Minute(), 0, 0, loc)
	if !next.After(now) {
		next = time.Date(year, month+1, 1, wall.Hour(), wall.Minute(), 0, 0, loc)
	}
	return next, nil
}

func advanceClientReset(client *model.Client, now time.Time) error {
	if client.ResetMonthlyTime == "" {
		client.NextReset = now.Unix() + int64(client.ResetDays)*86400
		return nil
	}
	next, err := nextMonthlyReset(now, client.ResetMonthlyTime, client.ResetTimezone)
	if err != nil {
		return err
	}
	client.NextReset = next.Unix()
	return nil
}

type clientResetUpdate struct {
	ManualResetAt *int64
	NextReset     *int64
}

func (s *ClientService) prepareClientReset(tx *gorm.DB, client *model.Client, editing bool, now time.Time, update clientResetUpdate) error {
	client.ResetMonthlyTime = strings.TrimSpace(client.ResetMonthlyTime)
	client.ResetTimezone = strings.TrimSpace(client.ResetTimezone)
	var previous model.Client
	if editing {
		if err := tx.Select("auto_reset", "delay_start", "reset_monthly_time", "reset_timezone", "next_reset", "manual_reset_at").
			First(&previous, client.Id).Error; err != nil {
			return err
		}
	}
	if update.ManualResetAt != nil {
		if *update.ManualResetAt != 0 && *update.ManualResetAt <= now.Unix() {
			return fmt.Errorf("choose a future date and time for the next reset")
		}
		client.ManualResetAt = *update.ManualResetAt
	} else {
		client.ManualResetAt = previous.ManualResetAt
	}
	client.NextReset = previous.NextReset
	if update.NextReset != nil {
		client.NextReset = *update.NextReset
	}
	if !client.AutoReset {
		client.NextReset = 0
		return nil
	}
	if client.ResetMonthlyTime == "" {
		if previous.ResetMonthlyTime != "" && !client.DelayStart {
			return advanceClientReset(client, now)
		}
		return nil
	}
	if client.DelayStart {
		client.NextReset = 0
	}
	next, err := nextMonthlyReset(now, client.ResetMonthlyTime, client.ResetTimezone)
	if err != nil {
		return err
	}
	if client.DelayStart {
		return nil
	}
	if previous.AutoReset && !previous.DelayStart && previous.ResetMonthlyTime == client.ResetMonthlyTime &&
		previous.ResetTimezone == client.ResetTimezone && previous.NextReset > 0 {
		client.NextReset = previous.NextReset
	} else {
		client.NextReset = next.Unix()
	}
	return nil
}
