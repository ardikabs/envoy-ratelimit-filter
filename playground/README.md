# Playground — local Envoy + rate-limit filter

Adoption demo: one `filter.so`, one global default plus per-route override,
standard `RateLimit-*` / `Retry-After` headers.

Config lives in `playground/envoy.yaml`:

- default: 5 req / 60s, bucket by `x-api-key` (absent key shares one bucket)
- route `premium-route` (`/premium`): 2 req / 60s, same bucket key

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
