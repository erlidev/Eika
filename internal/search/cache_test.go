package search

import (
	"testing"
	"time"
)

func TestCacheExpiresEntries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCache[string](4, time.Minute)
	c.Put("k", "v", start)

	if v, ok := c.Get("k", start.Add(59*time.Second)); !ok || v != "v" {
		t.Errorf("get before expiry = %q, %v; want v, true", v, ok)
	}
	if _, ok := c.Get("k", start.Add(time.Minute)); ok {
		t.Error("get at expiry found the entry")
	}
	if len(c.entries) != 0 || c.order.Len() != 0 {
		t.Error("an expired entry was kept")
	}
}

func TestCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewCache[int](2, time.Hour)
	c.Put("a", 1, now)
	c.Put("b", 2, now)
	c.Get("a", now) // a is now more recent than b
	c.Put("c", 3, now)

	if _, ok := c.Get("b", now); ok {
		t.Error("b survived, want it evicted as the least recently used")
	}
	for _, key := range []string{"a", "c"} {
		if _, ok := c.Get(key, now); !ok {
			t.Errorf("%s was evicted", key)
		}
	}
	c.Put("a", 10, now)
	if v, _ := c.Get("a", now); v != 10 {
		t.Errorf("a = %d after an overwrite, want 10", v)
	}
}
