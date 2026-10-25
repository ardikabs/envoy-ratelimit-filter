package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	mu    sync.Mutex
	count int64
	start int64 // unix nanos of window start
}

// Store holds fixed-window counters keyed by scope|bucket.
type Store struct {
	m sync.Map // string -> *bucket
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{}
}

func key(scope, bkt string) string {
	return scope + "\x00" + bkt
}

// Allow records one request for scope+bucket and reports allow/deny.
// Fixed window: when now-start >= interval, window resets.
func (s *Store) Allow(scope, bkt string, limit int, interval time.Duration, now time.Time) (allowed bool, remaining int, resetSec int) {
	if limit <= 0 || interval <= 0 {
		return true, limit, 0
	}
	k := key(scope, bkt)
	v, _ := s.m.LoadOrStore(k, &bucket{start: now.UnixNano()})
	b := v.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	nowNs := now.UnixNano()
	if nowNs-b.start >= int64(interval) {
		b.start = nowNs
		b.count = 0
	}
	resetSec = int((b.start + int64(interval) - nowNs + int64(time.Second) - 1) / int64(time.Second))
	if resetSec < 1 {
		resetSec = 1
	}
	if b.count < int64(limit) {
		b.count++
		return true, int(int64(limit) - b.count), resetSec
	}
	return false, 0, resetSec
}
