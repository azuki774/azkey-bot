package domain

import (
	"errors"
	"time"
)

var ErrUserStateNotFound = errors.New("user state not found")

// UserState stores check-in days, not the number of matching notes.
type UserState struct {
	CheckInDays     int
	ConsecutiveDays int
	LastCheckInAt   time.Time
}

// CheckInDay returns the beginning of the JST 05:00 business day.
func CheckInDay(at time.Time) time.Time {
	local := at.In(time.FixedZone("JST", 9*60*60)).Add(-5 * time.Hour)
	return time.Date(local.Year(), local.Month(), local.Day(), 5, 0, 0, 0, local.Location())
}

// CurrentStreak expires only after an entire day without a check-in.
func (s UserState) CurrentStreak(now time.Time) int {
	if s.LastCheckInAt.IsZero() || CheckInDay(now).Sub(CheckInDay(s.LastCheckInAt)) > 24*time.Hour {
		return 0
	}
	return s.ConsecutiveDays
}
