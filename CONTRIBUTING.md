# Contributing to otelcat

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — no protobuf compiler, no OTel SDK.

```bash
git clone https://github.com/JaydenCJ/otelcat && cd otelcat
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, starts it on a random loopback
port, delivers the demo payload in both OTLP encodings with
`examples/sendspan`, and asserts on the real rendered output; it must
finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (92 deterministic tests, no network, no sleeps).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (decoding and rendering never touch sockets — only
   `internal/server` and `internal/cli` do).

## Ground rules

- Keep dependencies at zero — the standard library covers everything
  otelcat does; adding one needs strong justification in the PR.
- No outbound network calls, ever. otelcat only *listens*, on loopback
  by default. No telemetry about telemetry.
- Wire-format knowledge is data: new OTLP fields get a field-number
  comment citing opentelemetry-proto, a decoder case, and a test built
  with the in-repo test encoder.
- Rendering must stay deterministic: identical input produces
  byte-identical output (UTC clocks, stable sort orders, no maps in
  output paths without sorting).
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `otelcat --version`, the full command line, and —
for decode problems — the exact payload if you can share it (an SDK
name + version is the next best thing, since payload shapes are
SDK-specific). For rendering problems, paste the plain-text output
(`--color never`).

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
