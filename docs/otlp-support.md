# OTLP support matrix

What otelcat 0.1.0 accepts, renders, and deliberately leaves out.
Field numbers reference [opentelemetry-proto](https://github.com/open-telemetry/opentelemetry-proto) v1.x.

## Transport

| Feature | Status |
|---|---|
| OTLP/HTTP `POST /v1/traces` | ✅ full render |
| OTLP/HTTP `POST /v1/logs` | ✅ full render |
| OTLP/HTTP `POST /v1/metrics` | ✅ accepted + summary line (data-point rendering is a roadmap item) |
| `application/x-protobuf` | ✅ hand-written wire decoder, no protobuf runtime |
| `application/json` | ✅ per the OTLP/JSON mapping, with SDK-divergence tolerance |
| `Content-Encoding: gzip` | ✅ decompressed-size cap applies (`--max-body`, default 16 MiB) |
| Success response | ✅ `{}` for JSON, empty message for protobuf, mirroring the request content type |
| Error response | ✅ `google.rpc.Status` JSON with code 3/12 + one stderr line |
| OTLP/gRPC (`:4317`) | ❌ roadmap — would require an HTTP/2 + gRPC framing layer |
| Retry/throttling signals (`Retry-After`) | ❌ not needed: otelcat never throttles |

## Trace fields

| Span field (number) | Rendered as |
|---|---|
| `trace_id` (1), `span_id` (2), `parent_span_id` (4) | trace grouping, tree edges, hex ids |
| `name` (5) | span line, `(unnamed span)` placeholder if empty |
| `kind` (6) | `INTERNAL` / `SERVER` / `CLIENT` / `PRODUCER` / `CONSUMER` tag |
| `start/end_time_unix_nano` (7, 8) | duration column + UTC clock in the trace header |
| `attributes` (9) | `key = value` lines (all seven AnyValue variants) |
| `events` (11) | `• +offset name attrs` lines under the span |
| `links` (13) | `↳ link trace=… span=…` lines |
| `status` (15) | `✓` / `✗ ERROR message` markers |
| `trace_state` (3), dropped counts (10/12/14), `flags` (16) | skipped (accepted, not rendered) |
| Resource `service.name` | trace header + per-span service context |
| Other resource attributes | rendered with `--resource` |
| Instrumentation scope | `name@version` in `--output json`; skipped in pretty mode |

## Log fields

| LogRecord field (number) | Rendered as |
|---|---|
| `time_unix_nano` (1), `observed_time_unix_nano` (11, fallback) | UTC clock column |
| `severity_number` (2) / `severity_text` (3) | level label (text wins; number maps to TRACE…FATAL bands) |
| `body` (5) | message (any AnyValue variant) |
| `attributes` (6) | `key=value` suffixes |
| `trace_id` (9), `span_id` (10) | `trace=…` correlation suffix |

## Known limits in 0.1.0

- Traces are grouped **per export batch**: a trace split across batches
  renders as multiple blocks (orphan spans are flagged with
  `(parent … not in this batch)` rather than dropped).
- Metric data points are counted, not rendered.
- Enum names in JSON must use the standard `SPAN_KIND_*` /
  `STATUS_CODE_*` / `SEVERITY_NUMBER_*` prefixes.
