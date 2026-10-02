package domain

import (
	"testing"
	"time"
)

func TestCurrentStreakJSTBoundary(t *testing.T) {
	last, _ := time.Parse(time.RFC3339, "2026-09-30T05:00:00+09:00")
	state := UserState{CheckInDays: 20, ConsecutiveDays: 3, LastCheckInAt: last}
	for _, tc := range []struct {
		at   string
		want int
	}{
		{"2026-09-30T23:59:59+09:00", 3},
		{"2026-10-01T04:59:59+09:00", 3},
		{"2026-10-01T05:00:00+09:00", 3},
		{"2026-10-02T04:59:59+09:00", 3},
		{"2026-10-01T20:00:00Z", 0},
	} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		if got := state.CurrentStreak(now); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.at, got, tc.want)
		}
	}
	if (UserState{}).CurrentStreak(last) != 0 {
		t.Fatal("empty state has a streak")
	}
}
