// Package render turns decoded OTLP payloads into terminal output.
// Three modes: pretty (per-trace tree, the default), compact (one line
// per span) and json (NDJSON, one flat object per span, for piping).
// Rendering is pure — no clocks, no globals — so identical input always
// produces byte-identical output.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/JaydenCJ/otelcat/internal/otlp"
)

// Mode selects the output style.
type Mode int

// Output modes.
const (
	ModePretty Mode = iota
	ModeCompact
	ModeJSON
)

// Options configures rendering. The zero value plus a Theme is the
// default pretty mode with attributes and events shown.
type Options struct {
	Mode          Mode
	Theme         Theme
	HideAttrs     bool   // suppress span/log attributes
	HideEvents    bool   // suppress span events
	ShowResource  bool   // also print resource attributes per trace
	Service       string // only render spans/logs from this service.name
	MinDurationNs uint64 // only render traces whose longest span reaches this
}

// Renderer writes decoded payloads to w. Not safe for concurrent use;
// the server serializes calls through a mutex.
type Renderer struct {
	w io.Writer
	o Options
}

// New returns a Renderer writing to w.
func New(w io.Writer, o Options) *Renderer { return &Renderer{w: w, o: o} }

// trace groups the spans of one trace id, in arrival order.
type trace struct {
	id    string
	spans []*otlp.Span
}

// Traces renders one export batch. It returns the number of spans
// actually printed (after filters); the shutdown summary deliberately
// counts *received* spans instead, so filters never hide volume.
func (r *Renderer) Traces(td *otlp.TraceData) int {
	spans := r.filterSpans(td.Spans)
	if len(spans) == 0 {
		return 0
	}
	switch r.o.Mode {
	case ModeJSON:
		for _, sp := range spans {
			r.spanJSON(sp)
		}
	case ModeCompact:
		for _, sp := range spans {
			r.spanCompact(sp)
		}
	default:
		for _, tr := range groupTraces(spans) {
			r.traceTree(tr)
		}
	}
	return len(spans)
}

func (r *Renderer) filterSpans(in []otlp.Span) []*otlp.Span {
	var out []*otlp.Span
	for i := range in {
		sp := &in[i]
		if r.o.Service != "" && sp.Service != r.o.Service {
			continue
		}
		out = append(out, sp)
	}
	if r.o.MinDurationNs == 0 {
		return out
	}
	// The duration gate keeps or drops whole traces: a slow trace's fast
	// child spans are exactly what you want to see, so we never punch
	// holes in a tree.
	keep := map[string]bool{}
	for _, sp := range out {
		if sp.DurationNano() >= r.o.MinDurationNs {
			keep[sp.TraceID] = true
		}
	}
	var kept []*otlp.Span
	for _, sp := range out {
		if keep[sp.TraceID] {
			kept = append(kept, sp)
		}
	}
	return kept
}

func groupTraces(spans []*otlp.Span) []*trace {
	var order []*trace
	byID := map[string]*trace{}
	for _, sp := range spans {
		tr, ok := byID[sp.TraceID]
		if !ok {
			tr = &trace{id: sp.TraceID}
			byID[sp.TraceID] = tr
			order = append(order, tr)
		}
		tr.spans = append(tr.spans, sp)
	}
	return order
}

// traceTree prints one trace: header line, then the span tree.
func (r *Renderer) traceTree(tr *trace) {
	th := r.o.Theme
	var startNs, endNs uint64
	services := map[string]bool{}
	for _, sp := range tr.spans {
		if startNs == 0 || (sp.StartTimeUnixNano != 0 && sp.StartTimeUnixNano < startNs) {
			startNs = sp.StartTimeUnixNano
		}
		if sp.EndTimeUnixNano > endNs {
			endNs = sp.EndTimeUnixNano
		}
		if sp.Service != "" {
			services[sp.Service] = true
		}
	}
	id := tr.id
	if id == "" {
		id = "(no trace id)"
	}
	head := th.Header("trace ") + th.Dim(id)
	if svc := joinSorted(services); svc != "" {
		head += "  " + th.Service(svc)
	}
	noun := "spans"
	if len(tr.spans) == 1 {
		noun = "span"
	}
	head += fmt.Sprintf("  %d %s", len(tr.spans), noun)
	if endNs > startNs {
		head += "  " + th.Duration(FormatDuration(endNs-startNs))
	}
	if startNs != 0 {
		head += "  " + th.Dim(clock(startNs))
	}
	fmt.Fprintln(r.w, head)

	if r.o.ShowResource {
		res := map[string]otlp.Attrs{}
		for _, sp := range tr.spans {
			if len(sp.ResourceAttrs) > 0 {
				res[sp.Service] = sp.ResourceAttrs
			}
		}
		for _, svc := range sortedKeys(res) {
			for _, kv := range res[svc] {
				fmt.Fprintf(r.w, "  %s %s\n", th.Dim("resource."+kv.Key+" ="), kv.Value.String())
			}
		}
	}

	roots, children := buildTree(tr.spans)
	width := nameColumnWidth(tr.spans, children)
	for _, root := range roots {
		r.spanNode(root, children, "", "", width)
	}
	fmt.Fprintln(r.w)
}

// buildTree returns root spans and a parent→children index. A span whose
// parent id is set but absent from the batch is treated as a root and
// flagged, so partial batches still render instead of vanishing.
func buildTree(spans []*otlp.Span) (roots []*otlp.Span, children map[string][]*otlp.Span) {
	present := map[string]bool{}
	for _, sp := range spans {
		if sp.SpanID != "" {
			present[sp.SpanID] = true
		}
	}
	children = map[string][]*otlp.Span{}
	for _, sp := range spans {
		if sp.ParentSpanID != "" && present[sp.ParentSpanID] && sp.ParentSpanID != sp.SpanID {
			children[sp.ParentSpanID] = append(children[sp.ParentSpanID], sp)
		} else {
			roots = append(roots, sp)
		}
	}
	sortSpans(roots)
	for _, c := range children {
		sortSpans(c)
	}
	return roots, children
}

// sortSpans orders siblings by start time; ties keep arrival order
// (sort.SliceStable) so output is deterministic for equal timestamps.
func sortSpans(spans []*otlp.Span) {
	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].StartTimeUnixNano < spans[j].StartTimeUnixNano
	})
}

// nameColumnWidth computes the width of the tree+name column so that
// durations line up per trace.
func nameColumnWidth(spans []*otlp.Span, children map[string][]*otlp.Span) int {
	depth := map[string]int{}
	var walk func(sp *otlp.Span, d int)
	walk = func(sp *otlp.Span, d int) {
		depth[sp.SpanID+sp.Name] = d
		for _, c := range children[sp.SpanID] {
			walk(c, d+1)
		}
	}
	for _, sp := range spans {
		if _, seen := depth[sp.SpanID+sp.Name]; !seen {
			walk(sp, 0)
		}
	}
	w := 0
	for _, sp := range spans {
		n := len([]rune(sp.Name)) + 3*depth[sp.SpanID+sp.Name]
		if n > w {
			w = n
		}
	}
	if w > 60 {
		w = 60
	}
	return w
}

// spanNode prints one span line plus its details and children.
// prefix is what the parent drew before this line; carry is what child
// lines below this node must be indented with.
func (r *Renderer) spanNode(sp *otlp.Span, children map[string][]*otlp.Span, branch, carry string, width int) {
	th := r.o.Theme
	name := sp.Name
	if name == "" {
		name = "(unnamed span)"
	}
	pad := width - len([]rune(name)) - len([]rune(branch))
	if pad < 0 {
		pad = 0
	}
	line := "  " + th.Dim(branch) + th.Name(name) + strings.Repeat(" ", pad)
	dur := FormatDuration(sp.DurationNano())
	line += "  " + padLeft(th.Duration(dur), dur, 7)
	line += "  " + pad8(th.Kind(sp.Kind.String()), sp.Kind.String())
	switch sp.StatusCode {
	case otlp.StatusError:
		msg := sp.StatusMessage
		if msg == "" {
			msg = "ERROR"
		} else {
			msg = "ERROR " + msg
		}
		line += "  " + th.Error("✗ "+msg)
	case otlp.StatusOK:
		line += "  " + th.OK("✓")
	}
	if sp.ParentSpanID != "" && branch == "" {
		line += "  " + th.Dim("(parent "+sp.ParentSpanID+" not in this batch)")
	}
	fmt.Fprintln(r.w, strings.TrimRight(line, " "))

	detail := "  " + carry + "     "
	if !r.o.HideAttrs {
		for _, kv := range sp.Attributes {
			fmt.Fprintln(r.w, detail+th.Dim(kv.Key+" =")+" "+kv.Value.String())
		}
	}
	if !r.o.HideEvents {
		for _, ev := range sp.Events {
			r.event(ev, sp.StartTimeUnixNano, detail)
		}
	}
	for _, ln := range sp.Links {
		fmt.Fprintln(r.w, detail+th.Dim("↳ link trace="+ln.TraceID+" span="+ln.SpanID))
	}

	kids := children[sp.SpanID]
	for i, c := range kids {
		cb, cc := "├─ ", "│  "
		if i == len(kids)-1 {
			cb, cc = "└─ ", "   "
		}
		r.spanNode(c, children, carry+cb, carry+cc, width)
	}
}

func (r *Renderer) event(ev otlp.Event, spanStart uint64, indent string) {
	th := r.o.Theme
	line := indent + th.Dim("•")
	if ev.TimeUnixNano >= spanStart && spanStart != 0 {
		line += " " + th.Duration("+"+FormatDuration(ev.TimeUnixNano-spanStart))
	}
	name := ev.Name
	if name == "exception" {
		name = th.Error(name)
	}
	line += " " + name
	for _, kv := range ev.Attributes {
		line += "  " + th.Dim(kv.Key+"=") + kv.Value.String()
	}
	fmt.Fprintln(r.w, line)
}

// spanCompact prints one span per line: duration, kind, service, name.
func (r *Renderer) spanCompact(sp *otlp.Span) {
	th := r.o.Theme
	svc := sp.Service
	if svc == "" {
		svc = "-"
	}
	name := sp.Name
	if name == "" {
		name = "(unnamed span)"
	}
	dur := FormatDuration(sp.DurationNano())
	line := padLeft(th.Duration(dur), dur, 7) +
		"  " + pad8(th.Kind(sp.Kind.String()), sp.Kind.String()) +
		"  " + th.Service(svc) +
		"  " + th.Name(name)
	if sp.TraceID != "" {
		line += "  " + th.Dim("trace="+short(sp.TraceID))
	}
	if sp.StatusCode == otlp.StatusError {
		line += "  " + th.Error("✗ "+strings.TrimSpace("ERROR "+sp.StatusMessage))
	}
	fmt.Fprintln(r.w, line)
}

// jsonSpan is the stable NDJSON shape (schema_version 1 — additive
// changes only within 0.x).
type jsonSpan struct {
	TraceID     string         `json:"traceId"`
	SpanID      string         `json:"spanId"`
	ParentID    string         `json:"parentSpanId,omitempty"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Service     string         `json:"service,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	StartNs     uint64         `json:"startTimeUnixNano"`
	EndNs       uint64         `json:"endTimeUnixNano"`
	DurationNs  uint64         `json:"durationNano"`
	Status      string         `json:"status"`
	StatusMsg   string         `json:"statusMessage,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
	EventNames  []string       `json:"events,omitempty"`
	ResourceMap map[string]any `json:"resource,omitempty"`
}

func (r *Renderer) spanJSON(sp *otlp.Span) {
	js := jsonSpan{
		TraceID:    sp.TraceID,
		SpanID:     sp.SpanID,
		ParentID:   sp.ParentSpanID,
		Name:       sp.Name,
		Kind:       sp.Kind.String(),
		Service:    sp.Service,
		Scope:      sp.Scope,
		StartNs:    sp.StartTimeUnixNano,
		EndNs:      sp.EndTimeUnixNano,
		DurationNs: sp.DurationNano(),
		Status:     sp.StatusCode.String(),
		StatusMsg:  sp.StatusMessage,
	}
	if !r.o.HideAttrs {
		js.Attributes = attrsToMap(sp.Attributes)
		if r.o.ShowResource {
			js.ResourceMap = attrsToMap(sp.ResourceAttrs)
		}
	}
	if !r.o.HideEvents {
		for _, ev := range sp.Events {
			js.EventNames = append(js.EventNames, ev.Name)
		}
	}
	b, err := json.Marshal(js)
	if err != nil {
		// A span that cannot marshal (unrepresentable float) must not
		// kill the stream; emit a diagnostic object instead.
		b = []byte(fmt.Sprintf(`{"error":%q}`, err.Error()))
	}
	fmt.Fprintln(r.w, string(b))
}

// attrsToMap converts attributes to a JSON-friendly map. Scalars keep
// their native JSON type; composites fall back to their string form.
func attrsToMap(attrs otlp.Attrs) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]any, len(attrs))
	for _, kv := range attrs {
		switch kv.Value.Kind {
		case otlp.ValueString:
			m[kv.Key] = kv.Value.Str
		case otlp.ValueBool:
			m[kv.Key] = kv.Value.Bool
		case otlp.ValueInt:
			m[kv.Key] = kv.Value.Int
		case otlp.ValueDouble:
			m[kv.Key] = kv.Value.Double
		default:
			m[kv.Key] = kv.Value.String()
		}
	}
	return m
}

// Logs renders one logs export batch; returns records printed.
func (r *Renderer) Logs(ld *otlp.LogData) int {
	printed := 0
	for i := range ld.Records {
		rec := &ld.Records[i]
		if r.o.Service != "" && rec.Service != r.o.Service {
			continue
		}
		printed++
		if r.o.Mode == ModeJSON {
			r.logJSON(rec)
		} else {
			r.logLine(rec)
		}
	}
	return printed
}

func (r *Renderer) logLine(rec *otlp.LogRecord) {
	th := r.o.Theme
	level := rec.Level()
	styled := level
	switch {
	case strings.HasPrefix(level, "ERROR"), strings.HasPrefix(level, "FATAL"):
		styled = th.Error(level)
	case strings.HasPrefix(level, "WARN"):
		styled = th.Warn(level)
	default:
		styled = th.OK(level)
	}
	line := th.Dim(clock(rec.TimeUnixNano)) + "  " + pad(styled, level, 5)
	if rec.Service != "" {
		line += "  " + th.Service(rec.Service)
	}
	if body := rec.Body.String(); body != "" {
		line += "  " + body
	}
	if !r.o.HideAttrs {
		for _, kv := range rec.Attributes {
			line += "  " + th.Dim(kv.Key+"=") + kv.Value.String()
		}
	}
	if rec.TraceID != "" {
		line += "  " + th.Dim("trace="+short(rec.TraceID))
	}
	fmt.Fprintln(r.w, line)
}

type jsonLog struct {
	TimeNs     uint64         `json:"timeUnixNano"`
	Level      string         `json:"level"`
	Service    string         `json:"service,omitempty"`
	Body       string         `json:"body"`
	Attributes map[string]any `json:"attributes,omitempty"`
	TraceID    string         `json:"traceId,omitempty"`
	SpanID     string         `json:"spanId,omitempty"`
}

func (r *Renderer) logJSON(rec *otlp.LogRecord) {
	js := jsonLog{
		TimeNs:  rec.TimeUnixNano,
		Level:   rec.Level(),
		Service: rec.Service,
		Body:    rec.Body.String(),
		TraceID: rec.TraceID,
		SpanID:  rec.SpanID,
	}
	if !r.o.HideAttrs {
		js.Attributes = attrsToMap(rec.Attributes)
	}
	b, _ := json.Marshal(js)
	fmt.Fprintln(r.w, string(b))
}

// Metrics prints the acknowledgement line for a metrics payload.
func (r *Renderer) Metrics(sum *otlp.MetricsSummary) {
	if r.o.Mode == ModeJSON {
		b, _ := json.Marshal(map[string]any{
			"metricCount": sum.MetricCount,
			"metricNames": sum.Names,
		})
		fmt.Fprintln(r.w, string(b))
		return
	}
	th := r.o.Theme
	names := ""
	if len(sum.Names) > 0 {
		shown := sum.Names
		if len(shown) > 6 {
			shown = shown[:6]
		}
		names = ": " + strings.Join(shown, ", ")
		if len(sum.Names) > 6 {
			names += ", …"
		}
	}
	noun := "metrics"
	if sum.MetricCount == 1 {
		noun = "metric"
	}
	fmt.Fprintf(r.w, "%s %d %s%s %s\n",
		th.Header("metrics"), sum.MetricCount, noun, names,
		th.Dim("(accepted; rendering data points is on the roadmap)"))
}

// clock formats a unix-nano timestamp as UTC HH:MM:SS.mmm — stable
// across machines, which keeps every test and demo byte-identical.
func clock(ns uint64) string {
	if ns == 0 {
		return "--:--:--.---"
	}
	t := time.Unix(0, int64(ns)).UTC()
	return t.Format("15:04:05.000")
}

func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}

// pad right-pads styled text to width based on its unstyled length,
// because ANSI escapes have zero display width.
func pad(styled, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled
}

// padLeft left-pads styled text to width based on its unstyled length.
func padLeft(styled, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return strings.Repeat(" ", n) + styled
	}
	return styled
}

// pad8 right-pads a styled span-kind tag to the widest kind name.
func pad8(styled, plain string) string { return pad(styled, plain, 8) }

func joinSorted(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedKeys(m map[string]otlp.Attrs) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
