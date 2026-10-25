# Header-Driven Local Rate-Limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the header-driven local rate-limit Go filter on gonvoy with global+per-route rules, exact RateLimit headers, and an Envoy playground.

**Architecture:** Pure limiter library in `internal/ratelimit` (config, fixed-window store, header matcher, gonvoy middleware) plus thin `main.go` registration; e2e fixture under `test/e2e/filters/ratelimit` using gonvoy suitekit and a manual `playground/`.

**Tech Stack:** Go 1.26, gonvoy @248bc5d (Envoy v1.40), Envoy contrib-v1.40 image, testify.

**Spec:** `docs/superpowers/specs/2026-10-25-header-ratelimit-design.md` — the plan argues from the spec, so the spec travels with it; executors read both.

## Global Constraints

- Go 1.26.10 per go.mod.
- github.com/ardikabs/gonvoy pinned at commit 248bc5dacb8158234a773e2a80aa7320a5ca58cc (Envoy v1.40 line).
- Envoy v1.40+ only; e2e image envoyproxy/envoy:contrib-v1.40-latest.
- Build filter.so with go build -o filter.so -buildmode=c-shared.
- Unit-tested code must never import pkg/envoy; registration stays in main.
- Exact header names: RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset, RateLimit-Policy, RateLimit, Retry-After (429 only).
- Fail-open: any filter/config failure passes through; never surface 500 from limit logic.
- Local only fixed-window counters, no background goroutines, p99 overhead under 1ms.

## Review Focus

- Burst of 2x limit in same millisecond still caps at limit and returns 429 with Retry-After and remaining 0 — most likely to bite under load test.
- x-api-key header name case variation (X-API-KEY vs x-api-key) still isolates to same bucket — reasonable person expects case-insensitive header names.
- Route with invalid per-route rule (limit 0) falls back to global default for that route only, other routes unaffected — spec implies per-scope fail-open.
- Upstream already sets RateLimit-* headers: our filter overwrites with authoritative values rather than duplicating — reasonable person expects single values.
- Sub-second interval (e.g. 500ms) still yields Reset ceiling of 1s, never 0 while window active — reasonable person expects backoff to work.

---

### Task 1: Config, matcher, and fixed-window store

**Files:**
- Create: `internal/ratelimit/config.go`
- Create: `internal/ratelimit/limiter.go`
- Test: `internal/ratelimit/config_test.go`
- Test: `internal/ratelimit/limiter_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `type HeaderMatch struct { Name, Exact, Prefix string }` + `func (m HeaderMatch) Matches(h http.Header) bool`
  - `type Rule struct { Limit int; Interval DurationSec; BucketKey string; MatchHeaders []HeaderMatch }`
  - `type Config struct { Default Rule; Routes map[string]Rule }`
  - `func ParseConfig(data []byte) (*Config, error)`
  - `func (c *Config) Resolve(routeName string) (rule *Rule, scopeKey string)`
  - `type Store struct` + `func NewStore() *Store` + `func (s *Store) Allow(scope, bucket string, limit int, interval time.Duration, now time.Time) (allowed bool, remaining int, resetSec int)`

- [ ] **Step 1: Write failing config test**

```go
func TestParseConfig_GlobalAndRoutes(t *testing.T) {
    cfg, err := ParseConfig([]byte(`{"default":{"limit":2,"interval":"60s","bucket_key":"x-api-key"},"routes":{"premium":{"limit":5,"interval":60,"bucket_key":"x-api-key"}}}`))
    require.NoError(t, err)
    require.Equal(t, 2, cfg.Default.Limit)
    require.Equal(t, "premium", func() string { _, sk := cfg.Resolve("premium"); return sk }())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ratelimit/ -run TestParseConfig_GlobalAndRoutes -v`
Expected: FAIL with "undefined: ParseConfig"

- [ ] **Step 3: Implement `internal/ratelimit/config.go` with HeaderMatch, Rule, Config, DurationSec custom UnmarshalJSON (number=seconds, string=Go duration or bare number), Matches (OR across entries, exact wins if both set, header name case-insensitive via http.Header.Get), Resolve (routes[route] else default; invalid per-route limit<=0 or interval<=0 falls back to default)**

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ratelimit/ -run TestParseConfig -v`
Expected: PASS

- [ ] **Step 5: Write failing limiter test**

```go
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
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/ratelimit/ -run TestStore_FixedWindow -v`
Expected: FAIL with "undefined: NewStore"

- [ ] **Step 7: Implement `internal/ratelimit/limiter.go` with sync.Map[string]*bucket (mutex, count, windowStart UnixNano), ceil resetSec, lazy reset when now-start>=interval, per-bucket mutex only**

- [ ] **Step 8: Run all Task 1 tests**

Run: `go test ./internal/ratelimit/ -v -race`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/ratelimit/config.go internal/ratelimit/limiter.go internal/ratelimit/config_test.go internal/ratelimit/limiter_test.go
git commit -m "feat: add ratelimit config and fixed-window store"
```

### Task 2: Gonvoy middleware with exact headers and fail-open

**Files:**
- Create: `internal/ratelimit/middleware.go`
- Test: `internal/ratelimit/middleware_test.go`

**Interfaces:**
- Consumes:
  - `ParseConfig`, `Config.Resolve`, `Store.Allow` from Task 1.
  - `gonvoy.Context`, `gonvoy.Handler`, `gonvoy.Middleware`.
- Produces:
  - `func RateLimit(cfg *Config, store *Store) gonvoy.Middleware`
  - `func SetRateLimitHeaders(c gonvoy.Context, limit, remaining, resetSec, windowSec int)`

- [ ] **Step 1: Write failing middleware test (allow then deny, headers exact)**

```go
func TestMiddleware_AllowsThenDeniesWithHeaders(t *testing.T) {
    cfg, _ := ParseConfig([]byte(`{"default":{"limit":1,"interval":"60s"}}`))
    h := RateLimit(cfg, NewStore())(gonvoy.Handler(func(c gonvoy.Context) error { return nil }))
    r1, _ := http.NewRequest("GET", "http://example.com/", nil)
    w1 := newRecordingWriter()
    require.NoError(t, h.ServeHTTP(w1, r1))
    require.Equal(t, "1", w1.Header().Get("RateLimit-Limit"))
    r2, _ := http.NewRequest("GET", "http://example.com/", nil)
    w2 := newRecordingWriter()
    require.NoError(t, h.ServeHTTP(w2, r2))
    require.Equal(t, 429, w2.Status())
    require.Equal(t, "0", w2.Header().Get("RateLimit-Remaining"))
    require.NotEmpty(t, w2.Header().Get("Retry-After"))
}
```

Note: test file defines local `newRecordingWriter` fake implementing `gonvoy.ResponseWriter` (Header/Write/WriteHeader/Status), never importing pkg/envoy.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ratelimit/ -run TestMiddleware_AllowsThenDenies -v`
Expected: FAIL with "undefined: RateLimit"

- [ ] **Step 3: Implement `internal/ratelimit/middleware.go`: resolve scope via c.Attr("xds.route_name","") else default; check MatchHeaders OR; bucket = c.Header(bucketKey) or "__shared__"; call store.Allow; set 5 headers + Retry-After on deny; on allow call next(c) then set headers; on any config/limit<=0/interval<=0 error return next(c) with no headers; recover panic to next(c); increment Stats counters; Log Info on deny only**

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ratelimit/ -run TestMiddleware -v`
Expected: PASS

- [ ] **Step 5: Add Review Focus tests to owning task (header case-insensitivity, invalid per-route fallback, overwrite upstream headers, sub-second reset ceiling)**

```go
func TestMiddleware_HeaderNameCaseInsensitive(t *testing.T) { /* X-API-KEY vs x-api-key same bucket */ }
func TestMiddleware_InvalidRouteRuleFallsBackToDefault(t *testing.T) { /* routes bad limit 0 uses default */ }
```

- [ ] **Step 6: Run full package with race**

Run: `go test ./internal/ratelimit/ -v -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/ratelimit/middleware.go internal/ratelimit/middleware_test.go
git commit -m "feat: add ratelimit middleware with standard headers"
```

### Task 3: Thin registration and build

**Files:**
- Create: `main.go`
- Modify: `go.mod` (add gonvoy pin + testify)
- Test: `go vet ./... && go test ./...`

**Interfaces:**
- Consumes: `RateLimit`, `ParseConfig`, `NewStore` from Tasks 1-2; `envoy.Handle`, `gonvoy.Use`, `gonvoy.WithConfig`, `gonvoy.ConfigOf`.
- Produces: `filter.so` buildable via `go build -o filter.so -buildmode=c-shared .`

- [ ] **Step 1: Pin gonvoy dependency**

Run: `go get github.com/ardikabs/gonvoy@248bc5dacb8158234a773e2a80aa7320a5ca58cc && go get github.com/stretchr/testify@v1.12.1 && go mod tidy`
Expected: go.mod contains gonvoy pseudo-version for 248bc5d, envoy v1.40 indirect.

- [ ] **Step 2: Write `main.go` (package main, init calls envoy.Handle("ratelimit", gonvoy.Use(wrapper), gonvoy.WithConfig(new(ratelimit.Config)), gonvoy.MetricPrefix("ratelimit_")) where wrapper reads gonvoy.ConfigOf[*ratelimit.Config](c) and delegates to shared store; on ConfigOf error returns nil for fail-open)**

- [ ] **Step 3: Verify vet + unit tests pass**

Run: `go vet ./... && go test ./... -race`
Expected: PASS, no pkg/envoy import in internal/ratelimit (verify with `go list -deps ./internal/ratelimit | grep pkg/envoy` empty).

- [ ] **Step 4: Verify c-shared build**

Run: `go build -o /tmp/filter.so -buildmode=c-shared . && ls -lh /tmp/filter.so`
Expected: PASS, .so produced.

- [ ] **Step 5: Commit**

```bash
git add main.go go.mod go.sum
git commit -m "feat: add thin envoy registration for ratelimit filter"
```

### Task 4: Automated e2e with suitekit against real Envoy

**Files:**
- Create: `test/e2e/filters/ratelimit/main.go`
- Create: `test/e2e/filters/ratelimit/envoy.yaml`
- Create: `test/e2e/filters/ratelimit/go.mod`
- Create: `test/e2e/go.mod`
- Create: `test/e2e/ratelimit_test.go`

**Interfaces:**
- Consumes: `main.go` filter logic, `internal/ratelimit` behavior, `suite.TestSuite`, `suite.TestCase`, `suite.TestSuiteKit`.
- Produces: `go test -tags e2e ./test/e2e/ -run TestRatelimit -v` green against contrib-v1.40 image.

- [ ] **Step 1: Create e2e filter main.go (imports pkg/envoy, Handle ratelimit with default limit 5/w=60s bucket x-api-key plus route premium limit 2, for e2e determinism)**

- [ ] **Step 2: Create envoy.yaml with two routes (route names global-route prefix / and premium-route prefix /premium, direct_response 200, golang filter library_id ratelimit plugin_name ratelimit with TypedStruct plugin_config matching main.go defaults)**

- [ ] **Step 3: Create go.mod files with replace github.com/ardikabs/gonvoy => ../../../ approx and pinned version; run go mod tidy in fixture**

- [ ] **Step 4: Write failing e2e test file with 4 cases: global N+1 gets 429 with Retry-After + RateLimit-*; per-route isolation (exhaust premium does not affect global); key isolation (different x-api-key independent); backoff headers present on allowed (RateLimit-Reset + Policy parseable)**

```go
//go:build e2e
func TestRatelimit(t *testing.T) { suite.NewTestSuite(suite.TestSuiteOptions{FilterLocation: "../filters/{filter}/{filename}"}).Run(t, RatelimitCases) }
```

- [ ] **Step 5: Build fixture and run e2e (requires docker)**

Run: `docker pull envoyproxy/envoy:contrib-v1.40-latest && (cd test/e2e/filters/ratelimit && go build -o filter.so -buildmode=c-shared .) && go test -tags e2e ./test/e2e/ -run TestRatelimit -v`
Expected: PASS (4 subtests). If docker unavailable in CI, test skips with log, not fail.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/filters/ratelimit/ test/e2e/ratelimit_test.go test/e2e/go.mod
git commit -m "test: add suitekit e2e for ratelimit filter"
```

### Task 5: Manual playground (adoption demo)

**Files:**
- Create: `playground/envoy.yaml`
- Create: `playground/run.sh`
- Create: `playground/curl.sh`
- Create: `playground/README.md`

**Interfaces:**
- Consumes: `filter.so` from Task 3, e2e envoy.yaml from Task 4.
- Produces: `./playground/run.sh` boots Envoy+filter on localhost:10000, `./playground/curl.sh` demonstrates allow/deny/header behavior.

- [ ] **Step 1: Write playground envoy.yaml (listener 10000, admin 8000, two routes with direct_response, golang filter pointing at /filter.so, default limit 5/60s for demo)**

- [ ] **Step 2: Write run.sh (builds filter.so, docker run envoyproxy/envoy:contrib-v1.40-latest with -v mounts, --concurrency 1, waits for /listeners on admin)**

- [ ] **Step 3: Write curl.sh (loops 7 requests showing RateLimit-Remaining decrement and 429 + Retry-After on 6th; second loop with different x-api-key showing independent bucket; premium route showing override)**

- [ ] **Step 4: Manual verify (requires docker; if unavailable, verify scripts shellcheck + yaml parse)**

Run: `bash -n playground/run.sh && bash -n playground/curl.sh && python3 -c "import yaml,sys; yaml.safe_load(open('playground/envoy.yaml'))" && echo OK`
Expected: OK

- [ ] **Step 5: Commit**

```bash
git add playground/
git commit -m "feat: add manual envoy playground for ratelimit adoption"
```
