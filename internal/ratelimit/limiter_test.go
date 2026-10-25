package ratelimit

import (
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
