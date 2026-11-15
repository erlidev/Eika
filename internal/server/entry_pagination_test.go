package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEntryPageQuery(t *testing.T) {
	for _, tc := range []struct {
		query string
		valid bool
		limit int
	}{
		{"", true, 50}, {"?limit=1&before=abcdefghijklmnopqrst", true, 1}, {"?limit=100", true, 100},
		{"?limit=0", false, 0}, {"?limit=101", false, 0}, {"?limit=01", false, 0},
		{"?before=notice-original", true, 50}, {"?before=" + strings.Repeat("x", 1024), true, 50}, {"?before=" + strings.Repeat("x", 1025), false, 0}, {"?before=", false, 0}, {"?before=%00", false, 0}, {"?limit=1;other=x", false, 0}, {"?limit=%zz", false, 0}, {"?before=notice-0123456789abcdef01234567", true, 50}, {"?before=%FF", false, 0}, {"?limit=1&limit=2", false, 0}, {"?other=x", false, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, limit, err := entryPageQuery(httptest.NewRequest("GET", "/"+tc.query, nil))
			if (err == nil) != tc.valid || (tc.valid && limit != tc.limit) {
				t.Fatalf("limit %d, error %v", limit, err)
			}
		})
	}
}
