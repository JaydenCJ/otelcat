# otelcat examples

## sendspan

A self-contained demo sender: it posts a deterministic four-span
checkout trace (including a failing payment call with an `exception`
event), two correlated log records, and one metrics batch to a running
otelcat. No OTel SDK required — the protobuf branch hand-encodes the
wire format, so the source doubles as a reference for what an OTLP/HTTP
exporter actually puts on the wire.

```bash
# terminal 1
otelcat

# terminal 2 — same payload, either encoding
go run ./examples/sendspan --encoding json
go run ./examples/sendspan --encoding protobuf
go run ./examples/sendspan --endpoint http://127.0.0.1:4318
```

Because the payload uses pinned timestamps and ids, both encodings
render byte-identically — which is exactly how `scripts/smoke.sh` uses
this program.

## Pointing a real SDK at otelcat

Any OpenTelemetry SDK works unmodified via the standard environment
variables (OTLP/HTTP; gRPC is a roadmap item):

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf   # or http/json
export OTEL_SERVICE_NAME=my-service
# then run your instrumented app; spans appear in the otelcat terminal
```

## Raw curl

The JSON encoding is easy to poke by hand:

```bash
curl -sS http://127.0.0.1:4318/v1/traces \
  -H 'Content-Type: application/json' \
  -d '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"curl-demo"}}]},"scopeSpans":[{"spans":[{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7","name":"hello","kind":1,"startTimeUnixNano":"1767366245000000000","endTimeUnixNano":"1767366245004200000"}]}]}]}'
```
