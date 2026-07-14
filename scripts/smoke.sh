#!/usr/bin/env bash
# End-to-end smoke test for otelcat: builds the binary, starts it on a
# random loopback port, delivers the demo payload in BOTH OTLP encodings
# with the bundled sender, and asserts on the real rendered output.
# Loopback only, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
SINK_PID=""
cleanup() {
  [ -n "$SINK_PID" ] && kill "$SINK_PID" 2>/dev/null || true
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  [ -f "$WORKDIR/stderr.log" ] && sed 's/^/  stderr> /' "$WORKDIR/stderr.log" >&2
  exit 1
}

BIN="$WORKDIR/otelcat"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/otelcat) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "otelcat 0.1.0" || fail "--version mismatch"

echo "3. usage errors exit 2"
set +e
"$BIN" --output yaml >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --output should exit 2"
set -e

echo "4. start the sink on a random loopback port"
"$BIN" --addr 127.0.0.1:0 --color never \
  >"$WORKDIR/stdout.log" 2>"$WORKDIR/stderr.log" &
SINK_PID=$!
PORT=""
for _ in $(seq 1 100); do
  PORT="$(grep -oE 'listening on http://127\.0\.0\.1:[0-9]+' "$WORKDIR/stderr.log" 2>/dev/null \
          | grep -oE '[0-9]+$' || true)"
  [ -n "$PORT" ] && break
  sleep 0.05
done
[ -n "$PORT" ] || fail "sink never reported its port"
ENDPOINT="http://127.0.0.1:$PORT"

echo "5. deliver the demo payload in both encodings"
(cd "$ROOT" && go run ./examples/sendspan --endpoint "$ENDPOINT" --encoding json) \
  || fail "json delivery failed"
(cd "$ROOT" && go run ./examples/sendspan --endpoint "$ENDPOINT" --encoding protobuf) \
  || fail "protobuf delivery failed"

echo "6. sink rejects garbage without dying"
CODE="$(cd "$ROOT" && go run ./scripts/httppost.go "$ENDPOINT/v1/traces" "application/json" "{nope")" \
  || fail "reject helper failed"
[ "$CODE" = "400" ] || fail "garbage JSON should get 400, got $CODE"
kill -0 "$SINK_PID" 2>/dev/null || fail "sink died on bad input"

echo "7. stop the sink and inspect its output"
kill -INT "$SINK_PID"
wait "$SINK_PID" 2>/dev/null || true
SINK_PID=""
OUT="$(cat "$WORKDIR/stdout.log")"
echo "$OUT" | grep -q "trace 4bf92f3577b34da6a3ce929d0e0e4736" || fail "trace header missing"
echo "$OUT" | grep -q "GET /api/checkout" || fail "root span missing"
echo "$OUT" | grep -q "├─ validate-cart" || fail "tree guides missing"
echo "$OUT" | grep -q "✗ ERROR card declined" || fail "error status missing"
echo "$OUT" | grep -q "• +40.2ms exception" || fail "span event missing"
echo "$OUT" | grep -q "payment failed" || fail "log record missing"
echo "$OUT" | grep -q "metrics 2 metrics" || fail "metrics summary missing"
# JSON and protobuf must render byte-identically: the payload appears twice.
[ "$(echo "$OUT" | grep -c 'trace 4bf92f3577b34da6a3ce929d0e0e4736')" = "2" ] \
  || fail "both encodings should render the same trace"
grep -q "6 requests, 8 spans, 4 log records, 4 metrics received" "$WORKDIR/stderr.log" \
  || fail "shutdown summary wrong"

echo "8. json output mode emits machine-readable NDJSON"
: >"$WORKDIR/stdout.log"; : >"$WORKDIR/stderr.log"
"$BIN" --addr 127.0.0.1:0 --output json --color always \
  >"$WORKDIR/stdout.log" 2>"$WORKDIR/stderr.log" &
SINK_PID=$!
PORT=""
for _ in $(seq 1 100); do
  PORT="$(grep -oE 'listening on http://127\.0\.0\.1:[0-9]+' "$WORKDIR/stderr.log" 2>/dev/null \
          | grep -oE '[0-9]+$' || true)"
  [ -n "$PORT" ] && break
  sleep 0.05
done
[ -n "$PORT" ] || fail "json sink never reported its port"
(cd "$ROOT" && go run ./examples/sendspan --endpoint "http://127.0.0.1:$PORT") \
  || fail "delivery to json sink failed"
kill -INT "$SINK_PID"; wait "$SINK_PID" 2>/dev/null || true
SINK_PID=""
grep -q '"name":"POST /payments"' "$WORKDIR/stdout.log" || fail "NDJSON span missing"
grep -q '"status":"ERROR"' "$WORKDIR/stdout.log" || fail "NDJSON status missing"
grep -q $'\x1b\[' "$WORKDIR/stdout.log" && fail "ANSI escapes leaked into json output"

echo "SMOKE OK"
