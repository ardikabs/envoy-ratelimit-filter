# envoy-ratelimit-filter

An implementation of an Envoy HTTP Filter built on top of the [Gonvoy](https://github.com/ardikabs/gonvoy) framework (`248bc5d`, Envoy v1.40 line): a header-driven local rate limiter.

Yet another opinionated local rate limiter for Envoy: no external services, fixed-window counters live in-process.

## How it works

Each request resolves to exactly one rule, then consumes one token from
that rule's fixed window:

1. **Config source** — Gonvoy merges listener and per-route plugin configs before the handler runs. With `RouteConfigWins`, a route carrying its
   own `ratelimit` config replaces the listener's outright (no merging). Routes without one inherit the listener config.
2. **Rule selection** — keyed `rules` are evaluated in sorted key order. The first match wins, else the reserved `default` (unconditional
   fallback). A rule with `match_headers` must match them; a rule without matchers is selected by its `bucket_key` header being present — but only
   when that bucket partitions differently from default's. A rule sharing default's bucket must declare `match_headers`, otherwise it is skipped.
   Invalid entries are skipped; an invalid `default` disables limiting (fail-open, never a `500`).
3. **Counting** — counters are per `route/ruleKey` scope plus the `bucket_key` header's value. Missing key header uses the shared bucket,
   never an error. Allowed requests forward upstream with quota headers, denied requests short-circuit `429` with no upstream call.

## Configuration

One typed config (`plugin_name: ratelimit`), same struct at listener and per-route level:

```yaml
plugin_config:
  "@type": type.googleapis.com/xds.type.v3.TypedStruct
  value:
    default:
      limit: 5
      interval: "60s"        # Go duration or bare seconds (60 also works)
      bucket_key: "x-api-key"
    rules:
      legacy:
        limit: 1
        interval: "60s"
        bucket_key: "x-api-key"
        match_headers:
          - name: "x-plan"
            exact: "legacy"  # or prefix: "trial-"
      per-tenant:
        limit: 50
        interval: "60s"
        bucket_key: "x-tenant-id"   # no matchers: presence selects it
```

Per-route override (replaces the whole listener set for that route):

```yaml
typed_per_filter_config:
  envoy.filters.http.golang:
    "@type": type.googleapis.com/envoy.extensions.filters.http.golang.v3alpha.ConfigsPerRoute
    plugins_config:
      ratelimit:
        config:
          "@type": type.googleapis.com/xds.type.v3.TypedStruct
          value:
            default:
              limit: 2
              interval: "60s"
              bucket_key: "x-api-key"
            rules:
              tenant:
                limit: 1
                interval: "60s"
                bucket_key: "x-tenant-id"
```

See `playground/envoy.yaml` for a complete working example.

## Response contract

Allowed and denied responses carry (exact names):

| Header | Meaning |
| --- | --- |
| `RateLimit-Limit` | max for the window |
| `RateLimit-Remaining` | left (`0` on deny) |
| `RateLimit-Reset` | seconds until reset |
| `RateLimit-Policy` | e.g. `5;w=60` |
| `RateLimit` | legacy compat, e.g. `limit=5, remaining=4, reset=60` |
| `Retry-After` | seconds, **only** on `429` |

Deny body is plain-text `rate limit exceeded`. Clients can back off on
`RateLimit-Reset` + `Retry-After` alone.

## Build / test

Requires Docker (linux `filter.so`; a macOS build is rejected by Envoy as
`invalid ELF header`) and Go 1.26.

```bash
make build       # linux filter.so at ./filter.so (via Docker)
make vet         # go vet (default + e2e tags)
make lint        # gofmt clean check
make test        # unit (race) + e2e vs real Envoy contrib-v1.40
make test-unit   # unit only
make test-e2e    # e2e only
make playground       # build + start Envoy on :10000 (detached)
make playground-demo  # curl allow/deny demo
make playground-down  # stop Envoy
make clean
```

Go commands run as `env -u GOROOT go` (a stale `GOROOT` export breaks the
`go` tool's version check).

## Layout

- `internal/ratelimit/` — pure filter logic, unit-tested, never imports `pkg/envoy`: `config.go` (keyed rules + selection), `limiter.go`
  (fixed-window store), `middleware.go` (Gonvoy layer + headers).
- `main.go` — thin registration only (`envoy.Handle`, `RouteConfigWins`).
- `test/e2e/` — [suitekit](https://github.com/ardikabs/gonvoy) cases booting real Envoy: global N+1, per-route isolation, key isolation,
  listener keyed rule, per-route multi-keyed, backoff headers.
- `playground/` — manual Envoy + `curl.sh` demo (mirrors the e2e config).
