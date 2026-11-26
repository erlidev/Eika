package schedule_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/schedule"
)

func TestNext(t *testing.T) {
	cases := []struct{ name, kind, expr, zone, after, want string }{
		{"weekday names", "cron", "30 7 * * mon-fri", "America/Denver", "2026-11-27T15:00:00Z", "2026-11-30T14:30:00Z"},
		{"spring gap", "cron", "30 2 * * *", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-09T06:30:00Z"},
		{"fall repeat", "cron", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"list range step", "cron", "0,30 8-12/2 * jan mon", "UTC", "2026-01-05T08:01:00Z", "2026-01-05T08:30:00Z"},
		{"one shot", "at", "2026-01-01T10:00:00-07:00", "America/Denver", "2026-01-01T00:00:00Z", "2026-01-01T17:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := schedule.Parse(tc.kind, tc.expr, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			after, err := time.Parse(time.RFC3339, tc.after)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Next(after).Format(time.RFC3339); got != tc.want {
				t.Fatalf("next = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestInvalid(t *testing.T) {
	for _, expr := range []string{"* * * *", "60 * * * *", "* * * * mon-funday", "*/0 * * * *"} {
		if _, err := schedule.Parse("cron", expr, "UTC"); err == nil {
			t.Errorf("accepted %q", expr)
		}
	}
}
