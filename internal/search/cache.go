package search

import (
	"container/list"
	"sync"
	"time"
)

// Cache keeps recent values for a while. Past its size it evicts the entry
// used least recently; past its lifetime an entry is gone.
type Cache[V any] struct {
	size int
	ttl  time.Duration

	mu      sync.Mutex
	entries map[string]*list.Element
	// order holds *cacheEntry values, most recently used at the front.
	order *list.List
}

// cacheEntry is one value and when it stops being served.
type cacheEntry[V any] struct {
	key     string
	value   V
	expires time.Time
}

// NewCache returns an empty cache of at most size entries, each kept for ttl.
func NewCache[V any](size int, ttl time.Duration) *Cache[V] {
	return &Cache[V]{size: size, ttl: ttl, entries: make(map[string]*list.Element), order: list.New()}
}

// Get returns the value stored under key, if it has not expired by now.
func (c *Cache[V]) Get(key string, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	entry := el.Value.(*cacheEntry[V])
	if !now.Before(entry.expires) {
		c.order.Remove(el)
		delete(c.entries, key)
		return zero, false
	}
	c.order.MoveToFront(el)
	return entry.value, true
}

// Put stores value under key from now on, evicting the least recently used
// entry when the cache is full.
func (c *Cache[V]) Put(key string, value V, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*cacheEntry[V])
		entry.value, entry.expires = value, now.Add(c.ttl)
		c.order.MoveToFront(el)
		return
	}
	c.entries[key] = c.order.PushFront(&cacheEntry[V]{key: key, value: value, expires: now.Add(c.ttl)})
	for c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry[V]).key)
	}
}

// Len returns how many entries the cache holds, expired ones included until
// they are next looked up.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
