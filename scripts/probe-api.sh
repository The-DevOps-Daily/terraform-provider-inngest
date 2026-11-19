#!/bin/sh
# Read-only probe of the Inngest endpoints the provider uses. Prints each
# response with key values and webhook URLs redacted, so the output can be
# shared. Usage: INNGEST_API_KEY=... scripts/probe-api.sh [environment]
set -eu
: "${INNGEST_API_KEY:?set INNGEST_API_KEY}"
base="${INNGEST_API_BASE_URL:-https://api.inngest.com}"
env="${1:-production}"

redact() {
  python3 -c '
import json, re, sys
raw = sys.stdin.read()
try:
    doc = json.loads(raw)
except ValueError:
    print(raw[:2000]); sys.exit(0)
def walk(v):
    if isinstance(v, dict):
        return {k: ("<redacted>" if k in ("key", "url") and isinstance(x, str) else walk(x)) for k, x in v.items()}
    if isinstance(v, list):
        return [walk(x) for x in v]
    return v
print(json.dumps(walk(doc), indent=2)[:6000])
'
}

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

call() {
  echo "== GET $*"
  status=$(curl -sS -o "$tmp" -w '%{http_code}' -H "Authorization: Bearer $INNGEST_API_KEY" "$@" || true)
  echo "status $status"
  redact < "$tmp"
  echo
}

call "$base/v2/envs?limit=250"
call "$base/v2/keys/signing"
call -H "X-Inngest-Env: $env" "$base/v2/keys/signing"
call -H "X-Inngest-Env: $env" "$base/v2/keys/events"
call -H "X-Inngest-Env: $env" "$base/v2/env/webhooks"
call "$base/v2/env/webhooks"
call -H "X-Inngest-Env: $env" "$base/v2/apps"
call "$base/v1/webhooks"
