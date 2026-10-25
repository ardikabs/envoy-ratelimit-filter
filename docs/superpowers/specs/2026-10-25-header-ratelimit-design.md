# Header-Driven Local Rate-Limit Filter — Design

Date: 2026-10-25
Status: approved — user said draft then straight to implementation, keep working until done
Scope: gonvoy @ 248bc5dacb8158234a773e2a80aa7320a5ca58cc, Envoy v1.40+, Go 1.26

## Builder check (2026-10-25, subagent review)

All six review questions settled as builder calls to keep moving (no human round per "keep working until done"):
1. match_headers OR semantics; exact wins if both exact+prefix set on one entry.
2. Validation fail-open per-scope: missing/invalid default means no limiting globally; invalid per-route rule falls back to default for that route only; limit<=0 or interval<=0 or unparsable interval means skip limiting for that scope.
3. Omitted/empty bucket_key means shared bucket by design, not an error; interval accepts JSON number (seconds) or JSON string Go duration ("60s","1m") or bare numeric string.
4. Multi-worker counting accepted as per-worker/per-process approximation in v1 (effective limit may multiply by worker count); documented limitation, matches local-only promise. e2e pins `--concurrency 1` via suitekit default.
5. Deny body exact: plain text `rate limit exceeded`, Content-Type text/plain via c.String.
6. Empty/unknown xds.route_name falls back to default; we overwrite (Set, not Add) our RateLimit-* headers as authoritative.

## Intent and why

Build an opinionated, per-instance local rate limiter as an Envoy Go HTTP filter on gonvoy so teams can adopt one `filter.so` with one global default plus per-route overrides, without running a global rate-limit service. Immediate goal is adoption (simple YAML, standard client headers). Benchmark comparison against Envoy's native local rate-limit is explicitly later, not part of v1.

My guess: you want the simplest thing that behaves correctly for header-keyed APIs and is easy to demo against real Envoy. Their words: nothing breaks today, this is a new opinionated implementation to compare later; adoption first, playground e2e with real Envoy via gonvoy suitekit.

## Goals

- Single `filter.so` (`plugin_name: ratelimit`), one global default plus per-route overrides by Envoy route name; per-route wins outright when present, no stacking.
- Header-driven rules: each rule declares match headers (name + exact/prefix), limit (requests), interval (seconds, e.g. 60), bucket key header (e.g. `x-api-key`); absent key value uses a single shared bucket, never errors.
- Exact response headers on allowed and denied: `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`, `RateLimit-Policy` (`{limit};w={windowSec}`), legacy `RateLimit` (`limit=.., remaining=.., reset=..`), plus `Retry-After` only on 429.
- Enforcement: allowed forwards upstream with headers added; denied short-circuits 429 with `limit exceeded` body, no upstream call.
- Fail-open: any filter/config failure passes through; limit logic never surfaces 500 (gonvoy panic default is 500, we avoid it by recovering to pass-through).
- Local only: in-process fixed-window counters, lost on restart.
- Playground: automated e2e via gonvoy `pkg/suite` suitekit against `envoyproxy/envoy:contrib-v1.40-*` plus a manual docker playground with curl.

## Non-goals

- Distributed counting, Redis, sliding-log precision, per-user dashboards, dynamic reload without Envoy reload.
- Benchmark harness or perf comparison vs native limiter (deferred).
- Per-route Envoy `typed_per_filter_config` merge semantics as primary config path (v1 uses one TypedStruct with a routes map).

## Anti-goals

- A technically passing limiter that fails adoption: requires code change per route, leaks 500s, affects an unrelated route, omits any of the six header names, or needs a custom client contract would be a failure even if counting is correct.

## Constraints (verbatim where pinned)

- Go 1.26.10 (`go.mod` says `go 1.26.10`).
- `github.com/ardikabs/gonvoy` pinned at commit `248bc5dacb8158234a773e2a80aa7320a5ca58cc` (Envoy v1.40 line, `envoyproxy/envoy v1.40.0` indirect).
- Envoy v1.40+ only; e2e image `envoyproxy/envoy:contrib-v1.40-latest`.
- Build: `go build -o filter.so -buildmode=c-shared`.
- Envoy golang filter uses `library_id`, `library_path: "/filter.so"`, `plugin_name: ratelimit`; plugin config is `xds.type.v3.TypedStruct` JSON.
- Unit-tested code must never import `pkg/envoy` (segfaults test binary); registration stays in `main`, handler logic in a library package importing only root `gonvoy`.
- p99 added latency under 1ms, no allocation blowup under burst (implies O(1) map lookup, no per-request allocs beyond header strings, no background goroutines).

## How it gets made (decisions locked)

- Framework shape: `envoy.Handle("ratelimit", gonvoy.Use(RateLimitMiddleware), gonvoy.WithConfig(...))` in `main`. Middleware calls `next(c)` on allow so after-`next` code can set response headers; returns `c.String(429, ...)` with headers on deny without calling `next`. Single-handler `return nil` cannot add response headers on the allowed path, so `Use` is required.
- Config shape (single TypedStruct, my call): listener-level JSON with `default` + `routes` map. Route resolved at request time via `c.Attr("xds.route_name", "")`. No `RouteConfigWins` / merge tags needed in v1. Example:
  `{"default":{"limit":100,"interval":"60s","bucket_key":"x-api-key","match_headers":[{"name":"x-plan","exact":"free"}]}, "routes":{"api-premium":{"limit":1000,"interval":"60s","bucket_key":"x-api-key"}}}`. Interval accepts Go duration string or bare seconds number.
- Resolution order: if `routes[routeName]` exists use it, else `default`. Within the chosen rule, if `match_headers` non-empty and none match, skip limiting (pass through with no RateLimit headers added by us). Exact match is case-sensitive on value; prefix is `strings.HasPrefix`; header name lookup is case-insensitive via `net/http` canonicalization. Missing key header value `""` maps to shared bucket key `"__shared__"`.
- Counting: fixed-window per `(scopeKey, bucketKey)` where `scopeKey = "global" or "route:<name>"`. In-memory `sync.Map[string]*bucket` with per-bucket mutex, `count int64 + windowStart UnixNano`. On request: lock, if `now - start >= interval` reset `start=now, count=0`; if `count < limit` allow (`count++`, remaining=`limit-count`), else deny (`remaining=0`). `resetSec = ceil((start+interval-now)/1s)`. Lazy expiry only; no sweeper goroutine. Fail-open: any error in Attr/Config/lock path returns `next(c)` with no headers.
- Headers (exact names/values): `RateLimit-Limit: <limit>`, `RateLimit-Remaining: <remaining>`, `RateLimit-Reset: <resetSec>`, `RateLimit-Policy: <limit>;w=<windowSec>`, `RateLimit: limit=<limit>, remaining=<remaining>, reset=<resetSec>`, `Retry-After: <resetSec>` on 429 only. Set via `c.SetHeader` before answering (deny) or after `next(c)` (allow). Deny body: plain text `rate limit exceeded` (my call, satisfies "states limit exceeded").
- Observability: `c.Stats().Counter("ratelimit_allowed_total"/"ratelimit_denied_total")`, `c.Log().Info` on deny only (avoid log spam). Metric prefix option `ratelimit_`.
- Testing split: library package `internal/ratelimit` (pure limiter + middleware, fully unit-testable with fake `gonvoy.ResponseWriter` + `http.Request`); `main.go` thin registration only; e2e fixture `test/e2e/filters/ratelimit/` (`main.go`, `envoy.yaml`, `go.mod` with `replace gonvoy => ...`) + case `test/e2e/tests/ratelimit.go`; manual playground `playground/` (docker compose or `docker run` + envoy.yaml + `curl.sh`).

## Left to builder

- Exact Go file layout, bucket struct/mutex granularity, `sync.Map` vs `map+RWMutex` choice, reset-second rounding edge (sub-second windows), header-name canonicalization details, and playground port numbers — settled in the plan within the constraints above.
- Benchmark harness stays out; leave hooks (counters, access logs) so it can be added later without changing the filter contract.
