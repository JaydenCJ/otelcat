# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- OTLP/HTTP receiver on `127.0.0.1:4318` with the three standard routes
  (`/v1/traces`, `/v1/logs`, `/v1/metrics`), both payload encodings
  (`application/x-protobuf` and `application/json`), gzip request
  bodies, spec-shaped success and `google.rpc.Status` error responses,
  and a decompressed-size body cap.
- Hand-written protobuf wire-format decoder (`internal/protowire`) —
  no protobuf runtime, no generated code, unknown fields skipped for
  forward compatibility with newer SDKs.
- OTLP/JSON decoder tolerant of real-SDK divergence: hex ids
  (uppercase normalized, all-zero treated as absent), 64-bit values as
  strings or numbers, enums as numbers or proto names, base64 bytes.
- Pretty trace rendering: per-trace span trees ordered by start time,
  aligned durations (`4.2µs`/`12.3ms`/`2m03s`), kind tags, status
  markers with messages, attributes, events with `+offset`, links, and
  orphan-span flagging for traces split across batches.
- Log rendering with severity bands, service, body, attributes and
  trace correlation; metrics acknowledged with a per-batch summary line.
- `--output compact` (one line per span) and `--output json` (stable
  NDJSON for `jq`), with telemetry on stdout and everything else on
  stderr so pipes stay clean.
- Filters and toggles: `--service`, `--min-duration` (whole-trace
  gate), `--no-attrs`, `--no-events`, `--resource`.
- Color themes honoring `--color auto|always|never` and `NO_COLOR`,
  with byte-identical content in plain and colored output.
- Shutdown summary (requests / spans / log records / metrics) on
  SIGINT/SIGTERM, and `--addr :0` random-port support that prints the
  bound address.
- Runnable demo sender (`examples/sendspan`) that hand-encodes both
  OTLP encodings, and an OTLP support matrix (`docs/otlp-support.md`).
- 92 deterministic offline tests (wire decoder, both payload decoders,
  renderer, in-process HTTP handler, CLI) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/otelcat/releases/tag/v0.1.0
