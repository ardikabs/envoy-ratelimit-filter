package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStore_FixedWindowBlocksNPlusOne(t *testing.T) {
	s := NewStore()
	now := time.Now()
	for i := 0; i < 2; i++ {
		ok, rem, _ := s.Allow("global", "shared", 2, time.Minute, now)
		require.True(t, ok)
		_ = rem
	}
	ok, rem, reset := s.Allow("global", "shared", 2, time.Minute, now)
	require.False(t, ok)
	require.Equal(t, 0, rem)
	require.Greater(t, reset, 0)
}

func TestStore_BurstCapsAtLimit(t *testing.T) {
	s := NewStore()
	now := time.Now()
	allowed := 0
	for i := 0; i < 10; i++ {
		ok, rem, reset := s.Allow("global", "burst", 5, time.Minute, now)
		if ok {
			allowed++
		} else {
			require.Equal(t, 0, rem)
			require.Greater(t, reset, 0)
		}
	}
	require.Equal(t, 5, allowed)
}

func TestStore_ConcurrentBurstCapsAtLimit(t *testing.T) {
	s := NewStore()
	now := time.Now()
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _, _ := s.Allow("global", "concurrent", 5, time.Minute, now); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int64(5), allowed.Load())
}
