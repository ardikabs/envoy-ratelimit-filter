package ratelimit

import (
	"net/http"
	"testing"
	"time"

	"github.com/ardikabs/gonvoy"
	"github.com/stretchr/testify/require"
)

type testWriter struct {
	hdr    http.Header
	status int
	body   []byte
}

func newRecordingWriter() *testWriter {
	return &testWriter{hdr: make(http.Header)}
}

func (w *testWriter) Header() http.Header { return w.hdr }
func (w *testWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.body = append(w.body, p...)
	return len(p), nil
}
func (w *testWriter) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}
func (w *testWriter) Status() int { return w.status }

func TestMiddleware_AllowsThenDeniesWithHeaders(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{"default":{"limit":1,"interval":"60s"}}`))
	h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))
	r1, _ := http.NewRequest("GET", "http://example.com/", nil)
	w1 := newRecordingWriter()
	h.ServeHTTP(w1, r1)
	require.Equal(t, "1", w1.Header().Get("RateLimit-Limit"))
	r2, _ := http.NewRequest("GET", "http://example.com/", nil)
	w2 := newRecordingWriter()
	h.ServeHTTP(w2, r2)
	require.Equal(t, 429, w2.Status())
	require.Equal(t, "0", w2.Header().Get("RateLimit-Remaining"))
	require.NotEmpty(t, w2.Header().Get("Retry-After"))
}

func TestMiddleware_HeaderNameCaseInsensitive(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{"default":{"limit":1,"interval":"60s","bucket_key":"x-api-key"}}`))
	store := NewStore()
	h := RateLimit(cfg, store)(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))
	r1, _ := http.NewRequest("GET", "http://example.com/", nil)
	r1.Header.Set("X-API-KEY", "k1")
	w1 := newRecordingWriter()
	h.ServeHTTP(w1, r1)
	require.Equal(t, "0", w1.Header().Get("RateLimit-Remaining"))

	r2, _ := http.NewRequest("GET", "http://example.com/", nil)
	r2.Header.Set("x-api-key", "k1")
	w2 := newRecordingWriter()
	h.ServeHTTP(w2, r2)
	require.Equal(t, 429, w2.Status())
}

func TestMiddleware_InvalidConfigPassesThrough(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{"default":{"limit":0,"interval":"60s"}}`))
	h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))
	r, _ := http.NewRequest("GET", "http://example.com/", nil)
	w := newRecordingWriter()
	h.ServeHTTP(w, r)
	require.Equal(t, 0, w.Status())
	require.Empty(t, w.Header().Get("RateLimit-Limit"))
}

func TestMiddleware_SelectsMatchingKeyedRule(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s"},
		"rules": {"free": {"limit":1,"interval":"60s","match_headers":[{"name":"x-plan","exact":"free"}]}}
	}`))
	h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))

	r1, _ := http.NewRequest("GET", "http://example.com/", nil)
	r1.Header.Set("x-plan", "free")
	w1 := newRecordingWriter()
	h.ServeHTTP(w1, r1)
	require.Equal(t, "1", w1.Header().Get("RateLimit-Limit"))

	// Same free-plan bucket exhausted: 429 with the rule's limit.
	r2, _ := http.NewRequest("GET", "http://example.com/", nil)
	r2.Header.Set("x-plan", "free")
	w2 := newRecordingWriter()
	h.ServeHTTP(w2, r2)
	require.Equal(t, 429, w2.Status())
	require.Equal(t, "1", w2.Header().Get("RateLimit-Limit"))

	// No matcher hit: default applies, untouched by free-plan traffic.
	r3, _ := http.NewRequest("GET", "http://example.com/", nil)
	w3 := newRecordingWriter()
	h.ServeHTTP(w3, r3)
	require.Equal(t, "100", w3.Header().Get("RateLimit-Limit"))
}

func TestMiddleware_DefaultAppliesWithoutBucketHeader(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{"default":{"limit":1,"interval":"60s","bucket_key":"x-api-key"}}`))
	h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))
	// No headers at all: default still enforces via the shared bucket.
	r, _ := http.NewRequest("GET", "http://example.com/", nil)
	w := newRecordingWriter()
	h.ServeHTTP(w, r)
	require.Equal(t, "1", w.Header().Get("RateLimit-Limit"))
}

func TestMiddleware_OverwritesUpstreamHeaders(t *testing.T) {
	cfg, _ := ParseConfig([]byte(`{"default":{"limit":10,"interval":"60s"}}`))
	h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error {
		c.SetHeader("RateLimit-Limit", "999")
		return nil
	}))
	r, _ := http.NewRequest("GET", "http://example.com/", nil)
	w := newRecordingWriter()
	h.ServeHTTP(w, r)
	require.Equal(t, "10", w.Header().Get("RateLimit-Limit"))
}

func TestMiddleware_SubSecondResetCeiling(t *testing.T) {
	s := NewStore()
	now := time.Now()
	ok, _, reset := s.Allow("global", "shared", 1, 500*time.Millisecond, now)
	require.True(t, ok)
	require.GreaterOrEqual(t, reset, 1)
}
