package ministry

import (
	"sync"
	"time"
)

// ttlEntry is a single cached value with an expiry timestamp.
type ttlEntry struct {
	value   any
	expires time.Time
}

// TTLCache is a goroutine-safe map with per-key TTL expiry.
// Values are stored as `any` so a single instance can host different return types.
type TTLCache struct {
	mu   sync.RWMutex
	data map[string]ttlEntry
	now  func() time.Time
}

// NewTTLCache returns an empty cache that uses time.Now as its clock.
func NewTTLCache() *TTLCache {
	return &TTLCache{
		data: make(map[string]ttlEntry),
		now:  time.Now,
	}
}

// Get returns the cached value if present and not expired.
func (c *TTLCache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[key]
	if !ok {
		return nil, false
	}
	if c.now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

// Set stores a value with the given TTL.
func (c *TTLCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = ttlEntry{value: value, expires: c.now().Add(ttl)}
}

// Invalidate removes a single key (used by /admin/cache/flush).
func (c *TTLCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
}

// Flush clears every entry.
func (c *TTLCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[string]ttlEntry)
}

// singleflight collapses concurrent identical fetches into one upstream call.
type sfCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

type singleflight struct {
	mu    sync.Mutex
	calls map[string]*sfCall
}

func newSingleflight() *singleflight {
	return &singleflight{calls: make(map[string]*sfCall)}
}

// Do runs fn at most once per concurrent key. Other callers wait for the
// in-flight result.
func (s *singleflight) Do(key string, fn func() (any, error)) (any, error) {
	s.mu.Lock()
	if c, ok := s.calls[key]; ok {
		s.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &sfCall{}
	c.wg.Add(1)
	s.calls[key] = c
	s.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	s.mu.Lock()
	delete(s.calls, key)
	s.mu.Unlock()

	return c.val, c.err
}
