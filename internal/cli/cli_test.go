// CLI tests: flag parsing, color resolution and the Run() paths that
// do not open a listener (version, usage errors). The full serve loop
// is covered end-to-end by scripts/smoke.sh.
package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/otelcat/internal/render"
	"github.com/JaydenCJ/otelcat/internal/version"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:4318" {
		t.Fatalf("default addr wrong: %q", cfg.Addr)
	}
	if cfg.Output != "pretty" || cfg.Color != "auto" {
		t.Fatalf("default output/color wrong: %q %q", cfg.Output, cfg.Color)
	}
	if cfg.MaxBody != 16<<20 {
		t.Fatalf("default max body wrong: %d", cfg.MaxBody)
	}
}

func TestParseConfigAllFlags(t *testing.T) {
	cfg, err := ParseConfig([]string{
		"--addr", "127.0.0.1:0", "--output", "json", "--color", "never",
		"--service", "checkout", "--min-duration", "150ms",
		"--no-attrs", "--no-events", "--resource", "--max-body", "1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:0" || cfg.Output != "json" || cfg.Color != "never" {
		t.Fatalf("flags wrong: %+v", cfg)
	}
	if cfg.Service != "checkout" || cfg.MinDuration != 150*time.Millisecond {
		t.Fatalf("filters wrong: %+v", cfg)
	}
	if !cfg.NoAttrs || !cfg.NoEvents || !cfg.Resource || cfg.MaxBody != 1024 {
		t.Fatalf("toggles wrong: %+v", cfg)
	}
}

func TestParseConfigRejectsBadValues(t *testing.T) {
	cases := [][]string{
		{"--output", "yaml"},
		{"--color", "rainbow"},
		{"--min-duration", "-1s"},
		{"--max-body", "0"},
		{"--addr", "no-port-here"},
		{"--no-such-flag"},
		{"positional"},
	}
	for _, args := range cases {
		if _, err := ParseConfig(args); err == nil {
			t.Errorf("args %v should be rejected", args)
		}
	}
}

func TestParseConfigAcceptsPortOnlyAddr(t *testing.T) {
	cfg, err := ParseConfig([]string{"--addr", ":4318"})
	if err != nil || cfg.Addr != ":4318" {
		t.Fatalf("port-only addr rejected: %v", err)
	}
}

func TestResolveColorMatrix(t *testing.T) {
	cases := []struct {
		mode              string
		isTTY, noColorSet bool
		want              bool
	}{
		{"auto", true, false, true},   // interactive terminal → color
		{"auto", false, false, false}, // piped → plain
		{"auto", true, true, false},   // NO_COLOR wins over TTY
		{"always", false, true, true}, // explicit always beats everything
		{"never", true, false, false}, // explicit never beats TTY
	}
	for _, c := range cases {
		if got := ResolveColor(c.mode, c.isTTY, c.noColorSet); got != c.want {
			t.Errorf("ResolveColor(%q, tty=%v, nocolor=%v) = %v, want %v",
				c.mode, c.isTTY, c.noColorSet, got, c.want)
		}
	}
}

func TestRenderOptionsMapping(t *testing.T) {
	cfg, err := ParseConfig([]string{"--output", "compact", "--min-duration", "2ms", "--no-attrs"})
	if err != nil {
		t.Fatal(err)
	}
	o := RenderOptions(cfg, true)
	if o.Mode != render.ModeCompact {
		t.Fatalf("mode wrong: %v", o.Mode)
	}
	if o.MinDurationNs != 2_000_000 {
		t.Fatalf("min duration wrong: %d", o.MinDurationNs)
	}
	if !o.HideAttrs || !o.Theme.Enabled() {
		t.Fatalf("options wrong: %+v", o)
	}
}

func TestRunVersionPrintsAndExitsZero(t *testing.T) {
	var out, errw bytes.Buffer
	code := Run([]string{"--version"}, &out, &errw)
	if code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	if strings.TrimSpace(out.String()) != "otelcat "+version.Version {
		t.Fatalf("version output wrong: %q", out.String())
	}
}

func TestRunUsageErrorExitsTwo(t *testing.T) {
	var out, errw bytes.Buffer
	code := Run([]string{"--output", "yaml"}, &out, &errw)
	if code != ExitUsage {
		t.Fatalf("exit code %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errw.String(), "yaml") {
		t.Fatalf("error should quote the bad value: %s", errw.String())
	}
	if out.Len() != 0 {
		t.Fatalf("usage errors must not write to stdout: %q", out.String())
	}
}

func TestRunHelpPrintsUsageAndExitsZero(t *testing.T) {
	var out, errw bytes.Buffer
	code := Run([]string{"--help"}, &out, &errw)
	if code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	// Requested help is the program's output: stdout, so it pipes.
	for _, want := range []string{"--addr", "--output", "--min-duration", "terminal sink for OTLP"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage missing %q", want)
		}
	}
	if errw.Len() != 0 {
		t.Fatalf("--help must not write to stderr: %q", errw.String())
	}
	// The stdout/stderr split is a documented interface (json | jq);
	// the help text must keep that promise spelled out.
	if !strings.Contains(Usage(), "stderr") || !strings.Contains(Usage(), "jq") {
		t.Fatal("usage text lost the stdout/stderr contract")
	}
}

func TestPluralNeverSaysOneRequests(t *testing.T) {
	// The shutdown summary must read naturally for every count.
	cases := []struct {
		n    int
		noun string
		want string
	}{
		{0, "span", "0 spans"},
		{1, "request", "1 request"},
		{2, "log record", "2 log records"},
		{1, "metric", "1 metric"},
	}
	for _, c := range cases {
		if got := plural(c.n, c.noun); got != c.want {
			t.Errorf("plural(%d, %q) = %q, want %q", c.n, c.noun, got, c.want)
		}
	}
}

func TestRunListenFailureExitsOne(t *testing.T) {
	// An unresolvable listen address fails fast with a runtime error.
	var out, errw bytes.Buffer
	code := Run([]string{"--addr", "203.0.113.1:1"}, &out, &errw)
	if code != ExitRuntime {
		t.Fatalf("exit code %d, want %d", code, ExitRuntime)
	}
	if !strings.Contains(errw.String(), "cannot listen") {
		t.Fatalf("listen error not reported: %s", errw.String())
	}
}
