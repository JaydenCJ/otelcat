// Package otlp defines otelcat's in-memory model of OTLP trace and log
// payloads, and decodes both wire encodings (protobuf and JSON) into it.
// The model is deliberately flat and renderer-friendly: hex-encoded ids,
// resolved enum names, and a single AnyValue type for every attribute.
package otlp

import "fmt"

// SpanKind mirrors opentelemetry.proto.trace.v1.Span.SpanKind.
type SpanKind int32

// Span kinds, in proto enum order.
const (
	KindUnspecified SpanKind = 0
	KindInternal    SpanKind = 1
	KindServer      SpanKind = 2
	KindClient      SpanKind = 3
	KindProducer    SpanKind = 4
	KindConsumer    SpanKind = 5
)

// String returns the short human name used in rendered output.
func (k SpanKind) String() string {
	switch k {
	case KindInternal:
		return "INTERNAL"
	case KindServer:
		return "SERVER"
	case KindClient:
		return "CLIENT"
	case KindProducer:
		return "PRODUCER"
	case KindConsumer:
		return "CONSUMER"
	default:
		return "UNSPECIFIED"
	}
}

// StatusCode mirrors opentelemetry.proto.trace.v1.Status.StatusCode.
type StatusCode int32

// Status codes, in proto enum order.
const (
	StatusUnset StatusCode = 0
	StatusOK    StatusCode = 1
	StatusError StatusCode = 2
)

// String returns the short human name used in rendered output.
func (c StatusCode) String() string {
	switch c {
	case StatusOK:
		return "OK"
	case StatusError:
		return "ERROR"
	default:
		return "UNSET"
	}
}

// ValueKind discriminates the variants of AnyValue.
type ValueKind int

// AnyValue variants.
const (
	ValueEmpty ValueKind = iota
	ValueString
	ValueBool
	ValueInt
	ValueDouble
	ValueArray
	ValueKVList
	ValueBytes
)

// AnyValue is a decoded opentelemetry.proto.common.v1.AnyValue.
type AnyValue struct {
	Kind   ValueKind
	Str    string
	Bool   bool
	Int    int64
	Double float64
	Bytes  []byte
	Array  []AnyValue
	KVList []KeyValue
}

// String renders the value the way otelcat prints attributes: bare
// scalars, JSON-ish arrays and maps, hex for bytes.
func (v AnyValue) String() string {
	switch v.Kind {
	case ValueString:
		return v.Str
	case ValueBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case ValueInt:
		return fmt.Sprintf("%d", v.Int)
	case ValueDouble:
		return fmt.Sprintf("%g", v.Double)
	case ValueBytes:
		return fmt.Sprintf("0x%x", v.Bytes)
	case ValueArray:
		s := "["
		for i, e := range v.Array {
			if i > 0 {
				s += ", "
			}
			s += e.String()
		}
		return s + "]"
	case ValueKVList:
		s := "{"
		for i, kv := range v.KVList {
			if i > 0 {
				s += ", "
			}
			s += kv.Key + "=" + kv.Value.String()
		}
		return s + "}"
	default:
		return ""
	}
}

// KeyValue is one attribute.
type KeyValue struct {
	Key   string
	Value AnyValue
}

// Attrs is an ordered attribute list (order preserved from the payload).
type Attrs []KeyValue

// Get returns the value for key and whether it was present.
func (a Attrs) Get(key string) (AnyValue, bool) {
	for _, kv := range a {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return AnyValue{}, false
}

// Event is a decoded Span.Event.
type Event struct {
	TimeUnixNano uint64
	Name         string
	Attributes   Attrs
}

// Link is a decoded Span.Link.
type Link struct {
	TraceID    string // hex
	SpanID     string // hex
	Attributes Attrs
}

// Span is a decoded span, flattened for rendering: ids are lowercase hex
// and resource/scope context is attached during decoding.
type Span struct {
	TraceID           string // 32 hex chars ("" if absent)
	SpanID            string // 16 hex chars
	ParentSpanID      string // "" for root spans
	Name              string
	Kind              SpanKind
	StartTimeUnixNano uint64
	EndTimeUnixNano   uint64
	Attributes        Attrs
	Events            []Event
	Links             []Link
	StatusCode        StatusCode
	StatusMessage     string

	// Context copied from the enclosing ResourceSpans / ScopeSpans.
	Service       string // resource attribute service.name, "" if unset
	ResourceAttrs Attrs
	Scope         string // "name" or "name@version"
}

// DurationNano returns end-start, or 0 when timestamps are missing or
// inverted (a clamp keeps a bad SDK from rendering as 584 years).
func (s *Span) DurationNano() uint64 {
	if s.EndTimeUnixNano <= s.StartTimeUnixNano {
		return 0
	}
	return s.EndTimeUnixNano - s.StartTimeUnixNano
}

// SeverityNumber mirrors opentelemetry.proto.logs.v1.SeverityNumber.
type SeverityNumber int32

// SeverityText returns the coarse level name for a severity number,
// following the OTLP severity bands (1-4 TRACE … 21-24 FATAL).
func (n SeverityNumber) SeverityText() string {
	switch {
	case n >= 1 && n <= 4:
		return "TRACE"
	case n >= 5 && n <= 8:
		return "DEBUG"
	case n >= 9 && n <= 12:
		return "INFO"
	case n >= 13 && n <= 16:
		return "WARN"
	case n >= 17 && n <= 20:
		return "ERROR"
	case n >= 21 && n <= 24:
		return "FATAL"
	default:
		return "UNSET"
	}
}

// LogRecord is a decoded log record with resource context attached.
type LogRecord struct {
	TimeUnixNano   uint64
	SeverityNumber SeverityNumber
	SeverityText   string
	Body           AnyValue
	Attributes     Attrs
	TraceID        string
	SpanID         string

	Service       string
	ResourceAttrs Attrs
	Scope         string
}

// Level returns the label to print: the record's own severity text when
// present, otherwise the band derived from the severity number.
func (r *LogRecord) Level() string {
	if r.SeverityText != "" {
		return r.SeverityText
	}
	return r.SeverityNumber.SeverityText()
}

// TraceData is one decoded ExportTraceServiceRequest.
type TraceData struct {
	Spans []Span
}

// LogData is one decoded ExportLogsServiceRequest.
type LogData struct {
	Records []LogRecord
}

// MetricsSummary is the shallow decode of an ExportMetricsServiceRequest:
// otelcat acknowledges metrics but does not render data points in 0.1.0.
type MetricsSummary struct {
	ResourceCount int
	MetricCount   int
	Names         []string // unique metric names, order of first appearance
}
