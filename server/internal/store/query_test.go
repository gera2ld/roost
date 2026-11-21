package store

import (
	"testing"
	"time"
)

func TestQueryDates(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 3, 31, 15, 0, 0, 0, time.UTC)
	c := &compiler{opt: SearchOptions{Loc: sh, Now: now}}
	for in, want := range map[string]time.Time{
		"2026-01-02": time.Date(2026, 1, 1, 16, 0, 0, 0, time.UTC), // the user's midnight
		"7d":         time.Date(2026, 3, 24, 15, 0, 0, 0, time.UTC),
		"2W":         time.Date(2026, 3, 17, 15, 0, 0, 0, time.UTC),
		"1m":         now.AddDate(0, -1, 0),
		"1y":         time.Date(2025, 3, 31, 15, 0, 0, 0, time.UTC),
	} {
		got, err := c.date(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("date(%q) = %v, %v; want %v", in, got.UTC(), err, want)
		}
	}
	for _, in := range []string{"yesterday", "2026-13-01", "7h", "-1d", "12345d"} {
		if _, err := c.date(in); !IsQueryError(err) {
			t.Errorf("date(%q): err = %v, want a query error", in, err)
		}
	}
}
