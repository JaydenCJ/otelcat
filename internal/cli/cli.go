// Package cli parses flags, resolves color mode, and runs the sink.
// Everything that can be pure is pure (ParseConfig, ResolveColor) so the
// interesting decisions are unit-testable without opening sockets.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JaydenCJ/otelcat/internal/render"
	"github.com/JaydenCJ/otelcat/internal/server"
	"github.com/JaydenCJ/otelcat/internal/version"
)

// Exit codes: 0 ok, 1 runtime error, 2 usage error.
const (
	ExitOK      = 0
	ExitRuntime = 1
	ExitUsage   = 2
)

// Config is the fully-resolved run configuration.
type Config struct {
	Addr        string
	Output      string // pretty | compact | json
	Color       string // auto | always | never
	NoAttrs     bool
	NoEvents    bool
	Resource    bool
	Service     string
	MinDuration time.Duration
	MaxBody     int64
	Version     bool
}

const usageText = `otelcat %s — terminal sink for OTLP: point your SDK at it, see spans instantly.

usage: otelcat [flags]

  --addr string          listen address (default "127.0.0.1:4318"; use :0 for a random port)
  --output string        pretty | compact | json  (default "pretty")
  --color string         auto | always | never    (default "auto"; NO_COLOR is honored)
  --service string       only show spans/logs whose service.name equals this
  --min-duration dur     only show traces containing a span at least this long (e.g. 100ms)
  --no-attrs             hide span and log attributes
  --no-events            hide span events
  --resource             also print resource attributes
  --max-body bytes       request body limit after decompression (default 16777216)
  --version              print version and exit

Spans, logs and metric summaries go to stdout; the banner, warnings and the
shutdown summary go to stderr, so 'otelcat --output json | jq' stays clean.
`

// ParseConfig parses argv (without the program name). It returns a
// usage error for unknown flags or invalid values; flag output is
// suppressed — the caller prints the message.
func ParseConfig(args []string) (*Config, error) {
	cfg := &Config{}
	fs := flag.NewFlagSet("otelcat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Addr, "addr", "127.0.0.1:4318", "")
	fs.StringVar(&cfg.Output, "output", "pretty", "")
	fs.StringVar(&cfg.Color, "color", "auto", "")
	fs.StringVar(&cfg.Service, "service", "", "")
	fs.DurationVar(&cfg.MinDuration, "min-duration", 0, "")
	fs.BoolVar(&cfg.NoAttrs, "no-attrs", false, "")
	fs.BoolVar(&cfg.NoEvents, "no-events", false, "")
	fs.BoolVar(&cfg.Resource, "resource", false, "")
	fs.Int64Var(&cfg.MaxBody, "max-body", server.DefaultMaxBody, "")
	fs.BoolVar(&cfg.Version, "version", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q (otelcat takes flags only)", fs.Arg(0))
	}
	switch cfg.Output {
	case "pretty", "compact", "json":
	default:
		return nil, fmt.Errorf("invalid --output %q (want pretty, compact or json)", cfg.Output)
	}
	switch cfg.Color {
	case "auto", "always", "never":
	default:
		return nil, fmt.Errorf("invalid --color %q (want auto, always or never)", cfg.Color)
	}
	if cfg.MinDuration < 0 {
		return nil, errors.New("--min-duration must not be negative")
	}
	if cfg.MaxBody <= 0 {
		return nil, errors.New("--max-body must be positive")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		return nil, fmt.Errorf("invalid --addr %q: %v", cfg.Addr, err)
	}
	return cfg, nil
}

// ResolveColor decides whether output should be colored, given the
// --color flag, whether stdout is a terminal, and the NO_COLOR variable
// (any non-empty value disables color unless --color=always insists).
func ResolveColor(mode string, isTTY, noColorSet bool) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		return isTTY && !noColorSet
	}
}

// RenderOptions converts a Config into renderer options.
func RenderOptions(cfg *Config, color bool) render.Options {
	o := render.Options{
		Theme:         render.NewTheme(color),
		HideAttrs:     cfg.NoAttrs,
		HideEvents:    cfg.NoEvents,
		ShowResource:  cfg.Resource,
		Service:       cfg.Service,
		MinDurationNs: uint64(cfg.MinDuration.Nanoseconds()),
	}
	switch cfg.Output {
	case "compact":
		o.Mode = render.ModeCompact
	case "json":
		o.Mode = render.ModeJSON
	}
	return o
}

// Run is the CLI entry point. stdout receives rendered telemetry only;
// stderr receives the banner, warnings and the shutdown summary.
func Run(args []string, stdout, stderr io.Writer) int {
	cfg, err := ParseConfig(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Explicitly requested help is the program's output, so it
			// goes to stdout (pipeable); unsolicited usage errors below
			// go to stderr like every other diagnostic.
			fmt.Fprintf(stdout, usageText, version.Version)
			return ExitOK
		}
		fmt.Fprintf(stderr, "otelcat: %v\nrun 'otelcat --help' for usage\n", err)
		return ExitUsage
	}
	if cfg.Version {
		fmt.Fprintf(stdout, "otelcat %s\n", version.Version)
		return ExitOK
	}

	isTTY := false
	if f, ok := stdout.(*os.File); ok {
		if st, err := f.Stat(); err == nil {
			isTTY = st.Mode()&os.ModeCharDevice != 0
		}
	}
	color := ResolveColor(cfg.Color, isTTY, os.Getenv("NO_COLOR") != "")

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		fmt.Fprintf(stderr, "otelcat: cannot listen on %s: %v\n", cfg.Addr, err)
		return ExitRuntime
	}

	renderer := render.New(stdout, RenderOptions(cfg, color))
	handler := server.New(renderer, stderr, cfg.MaxBody)
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	banner(stderr, ln.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-done
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "otelcat: server error: %v\n", err)
			return ExitRuntime
		}
	}

	st := handler.Stats()
	fmt.Fprintf(stderr, "otelcat: %s, %s, %s, %s received. bye.\n",
		plural(st.Requests, "request"), plural(st.Spans, "span"),
		plural(st.LogRecords, "log record"), plural(st.Metrics, "metric"))
	return ExitOK
}

// plural formats a count with its noun, adding "s" except for exactly 1,
// so a quiet session says "1 request", not "1 requests".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func banner(w io.Writer, addr string) {
	// Rewrite wildcard hosts so the printed URL is copy-pasteable.
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || host == "::" || host == "0.0.0.0" {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
	}
	fmt.Fprintf(w, "otelcat %s — listening on http://%s\n", version.Version, addr)
	fmt.Fprintf(w, "  traces  POST /v1/traces   logs  POST /v1/logs   metrics  POST /v1/metrics\n")
	fmt.Fprintf(w, "  encodings: application/x-protobuf, application/json (gzip accepted)\n")
	fmt.Fprintf(w, "point your SDK here:\n")
	fmt.Fprintf(w, "  export OTEL_EXPORTER_OTLP_ENDPOINT=http://%s\n", addr)
	fmt.Fprintf(w, "  export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf\n")
}

// Usage returns the formatted usage text (exported for tests).
func Usage() string { return fmt.Sprintf(usageText, version.Version) }
