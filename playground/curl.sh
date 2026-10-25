#!/usr/bin/env bash
set -uo pipefail
BASE="${BASE:-http://localhost:10000}"

say() { echo "### $*"; }
show() {
  local path="$1" key="${2:-}"
  local args=(-s -D - -o /dev/null)
  if [ -n "$key" ]; then args+=(-H "x-api-key: $key"); fi
  curl "${args[@]}" "$BASE$path" | grep -iE "^(HTTP|ratelimit|retry-after)" || true
  echo
}

say "1) Global default (limit 5/60s, no key -> shared bucket): 7 requests"
for i in 1 2 3 4 5 6 7; do echo "--- request $i /"; show /; done

say "2) Different x-api-key gets an independent bucket"
show / demo-key-A
show / demo-key-B

say "3) Per-route override: /premium limit 2/60s does not affect /"
for i in 1 2 3; do echo "--- /premium $i"; show /premium route-demo; done
echo "--- / with same key still allowed"
show / route-demo

say "Done. Inspect full headers with: curl -i $BASE/ -H 'x-api-key: demo'"
