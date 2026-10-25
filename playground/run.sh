#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> Building filter.so (linux) via golang:1.26.10-bookworm"
docker run --rm \
  -v "$ROOT:/app" -w /app \
  golang:1.26.10-bookworm \
  bash -c "GOFLAGS=-buildvcs=false go build -o /app/playground/filter.so -buildmode=c-shared ."

echo "==> Starting Envoy (contrib-v1.40) on :10000 (admin :8000)"
docker rm -f envoy-ratelimit-playground >/dev/null 2>&1 || true
docker run -d --name envoy-ratelimit-playground --rm \
  -p 8000:8000 -p 10000:10000 \
  -v "$ROOT/playground/envoy.yaml:/etc/envoy.yaml" \
  -v "$ROOT/playground/filter.so:/filter.so" \
  envoyproxy/envoy:contrib-v1.40-latest \
  /usr/local/bin/envoy -c /etc/envoy.yaml --log-level warn \
  --component-log-level main:error,golang:info,misc:error --concurrency 1 >/dev/null

echo -n "==> Waiting for Envoy admin"
for i in $(seq 1 50); do
  if curl -sf http://localhost:8000/listeners >/dev/null 2>&1; then
    echo " ready"
    echo "Try: ./playground/curl.sh"
    exit 0
  fi
  echo -n "."
  sleep 0.2
done
echo " FAILED"
docker logs envoy-ratelimit-playground 2>&1 | tail -20 || true
exit 1
