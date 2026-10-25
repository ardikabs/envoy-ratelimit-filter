# Product Requirement — Header-Driven Local Rate-Limit Filter (on Gonvoy)

## 1. Problem

Teams running Envoy need a simple per-instance rate limiter without operating
a global rate-limit service. They want different limits for different
routes/APIs, controlled by request headers (tenant, key, plan), and clients
that can read standard headers to back off correctly.

## 2. Users

- Platform engineer: installs one `filter.so`, sets one global default plus
  per-route overrides in Envoy YAML.
- API owner: sets limits for their route by route name, keyed by their own
  header (e.g. `x-api-key`, `x-tenant-id`).
- API client: reads `RateLimit-*` / `Retry-After` to throttle, no custom
  contract.

## 3. Core Promise

If a request exceeds its applicable limit, it gets `429` with correct
standard headers. If not, it passes through with the same headers showing
remaining quota. Limits apply locally per Envoy instance. Per-route rules
always win over global when both match.

## 4. Functional Requirements

### 4.1 Scope Hierarchy

- Global default limit applies to all routes unless overridden.
- Per-route limit (by Envoy route name) overrides global for that route.
- When both exist, per-route is authoritative. No stacking or adding.

### 4.2 Header-Driven Configuration

- Each rule declares: match headers (name plus exact/prefix match), limit
  (requests), interval (e.g. 60s), and bucket key (which header value
  isolates counters, e.g. `x-api-key` value; absent value means a single
  shared bucket for that rule).
- No code change to add a route. Only Envoy config.

### 4.3 Standard Response Headers (Names Must Be Exact)

- `RateLimit-Limit`: max in current window.
- `RateLimit-Remaining`: left in window, `0` on deny.
- `RateLimit-Reset`: seconds until window resets.
- `RateLimit-Policy`: e.g. `100;w=60` (limit plus window).
- `RateLimit` (legacy compat): e.g. `limit=100, remaining=0, reset=12`.
- `Retry-After`: seconds, only on `429`.
- Headers present on both allowed and denied responses.

### 4.4 Enforcement

- Allowed: forward to upstream unmodified except adding `RateLimit-*`.
- Denied: short-circuit `429`, body states limit exceeded, no upstream call.
- Independent counters per resolved key plus route. Missing key header does
  not error. It uses the shared bucket.

## 5. Non-Functional

- Local only: counters are per Envoy process, lost on restart. No
  cross-replica consistency promised.
- Overhead under 1ms added latency at p99, no allocation blowup under burst.
- No upstream dependency. Filter failure is fail-open to upstream, except
  limit logic errors which must never surface as `500`.

## 6. Acceptance

- Global limit blocks the N+1th request in a window with `429` plus correct
  `Retry-After`.
- Per-route override on one route does not affect another route.
- Two different `x-api-key` values get independent counters.
- Client can implement backoff using only `RateLimit-Reset` plus
  `Retry-After`.

## 7. Out of Scope (v1)

- Distributed counting, Redis, sliding-log precision, per-user dashboards,
  dynamic config reload without Envoy reload.
