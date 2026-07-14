// Renderer tests. All output assertions run with color disabled unless
// the test is *about* color, and every input is constructed in-memory,
// so results are byte-stable on every machine.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/otelcat/internal/otlp"
)

const testTrace = "4bf92f3577b34da6a3ce929d0e0e4736"

// span builds a test span; base keeps timestamps small and readable.
func span(name, spanID, parentID string, startMs, endMs uint64, kind otlp.SpanKind) otlp.Span {
	const base = uint64(1767366245000000000) // 2026-01-02T15:04:05Z
	return otlp.Span{
		TraceID:           testTrace,
		SpanID:            spanID,
		ParentSpanID:      parentID,
		Name:              name,
		Kind:              kind,
		StartTimeUnixNano: base + startMs*1_000_000,
		EndTimeUnixNano:   base + endMs*1_000_000,
		Service:           "checkout",
	}
}

func renderTraces(t *testing.T, o Options, spans ...otlp.Span) string {
	t.Helper()
	var buf bytes.Buffer
	New(&buf, o).Traces(&otlp.TraceData{Spans: spans})
	return buf.String()
}

func TestPrettyTraceHeaderLine(t *testing.T) {
	out := renderTraces(t, Options{}, span("root", "aa", "", 0, 128, otlp.KindServer))
	first := strings.SplitN(out, "\n", 2)[0]
	for _, want := range []string{"trace " + testTrace, "checkout", "1 span", "128ms", "15:04:05.000"} {
		if !strings.Contains(first, want) {
			t.Errorf("header %q missing %q", first, want)
		}
	}
}

func TestPrettyTreeGuidesAndOrder(t *testing.T) {
	out := renderTraces(t, Options{},
		span("root", "aa", "", 0, 100, otlp.KindServer),
		span("late-child", "cc", "aa", 50, 90, otlp.KindInternal),
		span("early-child", "bb", "aa", 10, 20, otlp.KindClient),
	)
	// Children sort by start time regardless of arrival order.
	early := strings.Index(out, "├─ early-child")
	late := strings.Index(out, "└─ late-child")
	if early == -1 || late == -1 {
		t.Fatalf("tree guides missing:\n%s", out)
	}
	if early > late {
		t.Fatalf("children not sorted by start time:\n%s", out)
	}
}

func TestPrettyNestedGrandchildIndentation(t *testing.T) {
	out := renderTraces(t, Options{},
		span("root", "aa", "", 0, 100, otlp.KindServer),
		span("child", "bb", "aa", 10, 90, otlp.KindInternal),
		span("grandchild", "cc", "bb", 20, 30, otlp.KindClient),
	)
	if !strings.Contains(out, "└─ child") {
		t.Fatalf("child guide missing:\n%s", out)
	}
	// The grandchild inherits the carry of its parent ("   " for a
	// last child) plus its own branch.
	if !strings.Contains(out, "   └─ grandchild") {
		t.Fatalf("grandchild indentation wrong:\n%s", out)
	}
}

func TestPrettyOrphanSpanFlagged(t *testing.T) {
	// A span whose parent is not in the batch (tail of a large trace
	// split across exports) must still render, with a note — silently
	// dropping it is exactly the failure mode otelcat exists to expose.
	out := renderTraces(t, Options{}, span("stray", "bb", "9999888877776666", 0, 10, otlp.KindInternal))
	if !strings.Contains(out, "stray") {
		t.Fatalf("orphan span dropped:\n%s", out)
	}
	if !strings.Contains(out, "(parent 9999888877776666 not in this batch)") {
		t.Fatalf("orphan note missing:\n%s", out)
	}
}

func TestPrettyDegenerateSpansStillRender(t *testing.T) {
	// A span claiming to be its own parent is invalid but must not
	// hang or overflow the stack.
	out := renderTraces(t, Options{}, span("loop", "aa", "aa", 0, 10, otlp.KindInternal))
	if strings.Count(out, "loop") != 1 {
		t.Fatalf("self-parent span rendered wrong:\n%s", out)
	}
	// A nameless span gets a visible placeholder, not an empty column.
	out = renderTraces(t, Options{}, span("", "bb", "", 0, 10, otlp.KindUnspecified))
	if !strings.Contains(out, "(unnamed span)") {
		t.Fatalf("placeholder missing:\n%s", out)
	}
}

func TestPrettyStatusMarkers(t *testing.T) {
	ok := span("fine", "aa", "", 0, 10, otlp.KindServer)
	ok.StatusCode = otlp.StatusOK
	bad := span("broken", "bb", "", 20, 30, otlp.KindServer)
	bad.StatusCode = otlp.StatusError
	bad.StatusMessage = "card declined"
	unset := span("meh", "cc", "", 40, 50, otlp.KindServer)

	out := renderTraces(t, Options{}, ok, bad, unset)
	if !strings.Contains(out, "✓") {
		t.Fatalf("OK marker missing:\n%s", out)
	}
	if !strings.Contains(out, "✗ ERROR card declined") {
		t.Fatalf("error marker missing:\n%s", out)
	}
	// UNSET status renders no marker at all.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "meh") && (strings.Contains(line, "✓") || strings.Contains(line, "✗")) {
			t.Fatalf("unset status must not render a marker: %q", line)
		}
	}
}

func TestPrettyAttributesAndHideAttrs(t *testing.T) {
	sp := span("op", "aa", "", 0, 10, otlp.KindInternal)
	sp.Attributes = otlp.Attrs{{Key: "http.route", Value: otlp.AnyValue{Kind: otlp.ValueString, Str: "/x"}}}
	if out := renderTraces(t, Options{}, sp); !strings.Contains(out, "http.route = /x") {
		t.Fatalf("attribute missing:\n%s", out)
	}
	if out := renderTraces(t, Options{HideAttrs: true}, sp); strings.Contains(out, "http.route") {
		t.Fatalf("--no-attrs leaked attributes:\n%s", out)
	}
}

func TestPrettyEventWithOffsetAndHideEvents(t *testing.T) {
	sp := span("op", "aa", "", 0, 100, otlp.KindInternal)
	sp.Events = []otlp.Event{{
		TimeUnixNano: sp.StartTimeUnixNano + 40_200_000,
		Name:         "exception",
		Attributes:   otlp.Attrs{{Key: "exception.type", Value: otlp.AnyValue{Kind: otlp.ValueString, Str: "Boom"}}},
	}}
	out := renderTraces(t, Options{}, sp)
	if !strings.Contains(out, "• +40.2ms exception  exception.type=Boom") {
		t.Fatalf("event line wrong:\n%s", out)
	}
	if out := renderTraces(t, Options{HideEvents: true}, sp); strings.Contains(out, "exception") {
		t.Fatalf("--no-events leaked events:\n%s", out)
	}
}

func TestPrettyResourceAttrsOnlyWithFlag(t *testing.T) {
	sp := span("op", "aa", "", 0, 10, otlp.KindInternal)
	sp.ResourceAttrs = otlp.Attrs{{Key: "host.name", Value: otlp.AnyValue{Kind: otlp.ValueString, Str: "dev-box"}}}
	if out := renderTraces(t, Options{}, sp); strings.Contains(out, "host.name") {
		t.Fatalf("resource attrs shown without --resource:\n%s", out)
	}
	out := renderTraces(t, Options{ShowResource: true}, sp)
	if !strings.Contains(out, "resource.host.name = dev-box") {
		t.Fatalf("resource attrs missing with --resource:\n%s", out)
	}
}

func TestServiceFilterDropsOtherServices(t *testing.T) {
	mine := span("mine", "aa", "", 0, 10, otlp.KindServer)
	theirs := span("theirs", "bb", "", 0, 10, otlp.KindServer)
	theirs.Service = "noisy-neighbor"
	out := renderTraces(t, Options{Service: "checkout"}, mine, theirs)
	if !strings.Contains(out, "mine") || strings.Contains(out, "theirs") {
		t.Fatalf("service filter wrong:\n%s", out)
	}
}

func TestMinDurationKeepsWholeTraceNotJustSlowSpans(t *testing.T) {
	// The fast child of a slow trace is kept: the gate is per-trace.
	out := renderTraces(t, Options{MinDurationNs: 50_000_000},
		span("slow-root", "aa", "", 0, 100, otlp.KindServer),
		span("fast-child", "bb", "aa", 1, 2, otlp.KindInternal),
	)
	if !strings.Contains(out, "fast-child") {
		t.Fatalf("fast child of slow trace dropped:\n%s", out)
	}
	// An all-fast trace is dropped entirely.
	out = renderTraces(t, Options{MinDurationNs: 50_000_000},
		span("quick", "cc", "", 0, 3, otlp.KindServer))
	if out != "" {
		t.Fatalf("fast trace should be silent, got:\n%s", out)
	}
}

func TestCompactModeOneLinePerSpan(t *testing.T) {
	bad := span("POST /pay", "bb", "aa", 0, 89, otlp.KindClient)
	bad.StatusCode = otlp.StatusError
	out := renderTraces(t, Options{Mode: ModeCompact}, span("GET /x", "aa", "", 0, 128, otlp.KindServer), bad)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "128ms") || !strings.Contains(lines[0], "SERVER") ||
		!strings.Contains(lines[0], "checkout") || !strings.Contains(lines[0], "trace=4bf92f35…") {
		t.Fatalf("compact line wrong: %q", lines[0])
	}
	if !strings.Contains(lines[1], "✗ ERROR") {
		t.Fatalf("compact error marker missing: %q", lines[1])
	}
}

func TestJSONModeEmitsValidNDJSON(t *testing.T) {
	sp := span("op", "aa", "", 0, 128, otlp.KindServer)
	sp.Attributes = otlp.Attrs{
		{Key: "n", Value: otlp.AnyValue{Kind: otlp.ValueInt, Int: 7}},
		{Key: "ok", Value: otlp.AnyValue{Kind: otlp.ValueBool, Bool: true}},
	}
	out := renderTraces(t, Options{Mode: ModeJSON}, sp)
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if obj["name"] != "op" || obj["kind"] != "SERVER" || obj["durationNano"] != float64(128_000_000) {
		t.Fatalf("json fields wrong: %v", obj)
	}
	attrs := obj["attributes"].(map[string]any)
	if attrs["n"] != float64(7) || attrs["ok"] != true {
		t.Fatalf("attribute types not native JSON: %v", attrs)
	}
}

func TestColorEnabledWrapsAndDisabledDoesNot(t *testing.T) {
	sp := span("op", "aa", "", 0, 10, otlp.KindServer)
	plain := renderTraces(t, Options{}, sp)
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain output has ANSI codes:\n%q", plain)
	}
	colored := renderTraces(t, Options{Theme: NewTheme(true)}, sp)
	if !strings.Contains(colored, "\x1b[36mop\x1b[0m") {
		t.Fatalf("span name not cyan in colored output:\n%q", colored)
	}
	// Stripping the escapes must yield the plain rendering: color is
	// presentation only, never content.
	if stripANSI(colored) != plain {
		t.Fatalf("colored and plain differ beyond escapes:\n%q\n%q", stripANSI(colored), plain)
	}
	// And --output json | jq must never see escape codes at all,
	// whatever --color says.
	jsonOut := renderTraces(t, Options{Mode: ModeJSON, Theme: NewTheme(true)}, sp)
	if strings.Contains(jsonOut, "\x1b[") {
		t.Fatalf("ANSI codes in JSON output:\n%q", jsonOut)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestTracesReturnsPrintedCount(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, Options{Service: "checkout"})
	other := span("x", "bb", "", 0, 10, otlp.KindServer)
	other.Service = "other"
	n := r.Traces(&otlp.TraceData{Spans: []otlp.Span{
		span("a", "aa", "", 0, 10, otlp.KindServer), other,
	}})
	if n != 1 {
		t.Fatalf("want 1 printed span, got %d", n)
	}
}

func TestLogsPrettyLine(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, Options{})
	r.Logs(&otlp.LogData{Records: []otlp.LogRecord{{
		TimeUnixNano:   1767366245005000000,
		SeverityNumber: 17,
		Body:           otlp.AnyValue{Kind: otlp.ValueString, Str: "payment failed"},
		Attributes:     otlp.Attrs{{Key: "order.id", Value: otlp.AnyValue{Kind: otlp.ValueInt, Int: 8123}}},
		TraceID:        testTrace,
		Service:        "checkout",
	}}})
	out := buf.String()
	for _, want := range []string{"15:04:05.005", "ERROR", "checkout", "payment failed", "order.id=8123", "trace=4bf92f35…"} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %q:\n%s", want, out)
		}
	}
}

func TestLogsSeverityTextWinsAndZeroClockPlaceholder(t *testing.T) {
	// The record's own severityText outranks the number band, and a
	// record with no timestamp gets a visible placeholder column.
	var buf bytes.Buffer
	New(&buf, Options{}).Logs(&otlp.LogData{Records: []otlp.LogRecord{{
		SeverityNumber: 9, SeverityText: "NOTICE",
		Body: otlp.AnyValue{Kind: otlp.ValueString, Str: "x"},
	}}})
	if !strings.Contains(buf.String(), "NOTICE") {
		t.Fatalf("severityText should win:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "--:--:--.---") {
		t.Fatalf("zero timestamp placeholder missing:\n%s", buf.String())
	}
}

func TestLogsJSONMode(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, Options{Mode: ModeJSON}).Logs(&otlp.LogData{Records: []otlp.LogRecord{{
		TimeUnixNano: 5, SeverityNumber: 9,
		Body: otlp.AnyValue{Kind: otlp.ValueString, Str: "hello"},
	}}})
	var obj map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["level"] != "INFO" || obj["body"] != "hello" {
		t.Fatalf("log json wrong: %v", obj)
	}
}

func TestMetricsSummaryLineTruncatesNames(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, Options{}).Metrics(&otlp.MetricsSummary{
		MetricCount: 8,
		Names:       []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"},
	})
	out := buf.String()
	if !strings.Contains(out, "8 metrics") || !strings.Contains(out, "m6, …") {
		t.Fatalf("metrics summary wrong:\n%s", out)
	}
	if strings.Contains(out, "m7") {
		t.Fatalf("names beyond 6 should be elided:\n%s", out)
	}
	// In JSON mode the acknowledgement is NDJSON too — never ANSI text.
	var jbuf bytes.Buffer
	New(&jbuf, Options{Mode: ModeJSON, Theme: NewTheme(true)}).Metrics(&otlp.MetricsSummary{
		MetricCount: 2, Names: []string{"a", "b"},
	})
	if !strings.Contains(jbuf.String(), `"metricCount":2`) || strings.Contains(jbuf.String(), "\x1b[") {
		t.Fatalf("metrics json wrong:\n%q", jbuf.String())
	}
}

func TestRenderingIsDeterministic(t *testing.T) {
	// Two services in one trace exercise the sorted service join, and a
	// second trace id exercises grouping; two runs must be byte-identical.
	a := span("a", "aa", "", 0, 10, otlp.KindServer)
	b := span("b", "bb", "", 5, 8, otlp.KindServer)
	b.Service = "billing"
	c := span("other-root", "dd", "", 0, 10, otlp.KindServer)
	c.TraceID = "ffffffffffffffffffffffffffffffff"
	first := renderTraces(t, Options{}, a, b, c)
	second := renderTraces(t, Options{}, a, b, c)
	if first != second {
		t.Fatal("rendering not deterministic")
	}
	if !strings.Contains(first, "billing, checkout") {
		t.Fatalf("services not sorted:\n%s", first)
	}
	if strings.Count(first, "trace ") != 2 {
		t.Fatalf("want two trace headers:\n%s", first)
	}
}
