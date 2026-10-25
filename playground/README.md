# Playground — local Envoy + rate-limit filter

Adoption demo: one `filter.so`, one global default plus per-route override,
standard `RateLimit-*` / `Retry-After` headers.

Config lives in `playground/envoy.yaml` — one typed config with a reserved
`default` fallback plus keyed `rules` (first sorted-key match wins):

- listener `plugin_config`: `default` 5 req / 60s by `x-api-key`
  (absent key shares one bucket), plus keyed rule `legacy`: 1 req / 60s
  when `x-plan: legacy`.
- route `premium-route` (`/premium`) `typed_per_filter_config` override
  (replaces the listener config outright via `RouteConfigWins`, no merging):
  `default` 2 req / 60s by `x-api-key`, plus keyed rule `tenant`:
  1 req / 60s by `x-tenant-id` when that header is present.

## Run

```bash
./playground/run.sh   # builds playground/filter.so (linux) and starts Envoy on :10000
./playground/curl.sh  # allow/deny demo with headers
docker stop envoy-ratelimit-playground
```

## What to look at

- 6th request to `/` returns `429` with `RateLimit-Remaining: 0` and `Retry-After`.
- Different `x-api-key` values get independent counters.
- Exhausting `/premium` does not affect `/` for the same key.
- Allowed responses also carry `RateLimit-Limit/Remaining/Reset/Policy` and legacy `RateLimit`.

## Automated version

```bash
go test -tags e2e ./test/e2e/ -run TestRatelimit -v
```

The e2e build must run in Docker (linux `filter.so`); a macOS-built `.so`
is Mach-O and Envoy reports `invalid ELF header`. `run.sh` handles this.
