// JSON decoding for OTLP/HTTP with Content-Type application/json.
// The OTLP/JSON mapping has sharp edges this file exists to absorb:
// ids are hex (not the base64 of vanilla proto3-JSON), 64-bit integers
// and timestamps arrive as strings OR numbers depending on the SDK, and
// enums arrive as numbers OR their full proto names.
package otlp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// flexUint64 accepts 123, "123" and the exponent-free float form some
// JSON emitters produce for nanosecond timestamps.
type flexUint64 uint64

func (f *flexUint64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		// Some emitters serialize uint64 as a JSON float (e.g. 1.7e+18).
		// Reject values outside [0, 2^64): converting those to uint64
		// is implementation-defined in Go, not an error.
		fv, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil || fv < 0 || fv >= 1<<64 {
			return fmt.Errorf("invalid uint64 %q", s)
		}
		v = uint64(fv)
	}
	*f = flexUint64(v)
	return nil
}

// flexInt64 accepts 123 and "123" (the OTLP/JSON mapping requires int64
// attribute values to be strings, but real SDKs send both).
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid int64 %q", s)
	}
	*f = flexInt64(v)
	return nil
}

// flexEnum accepts 2 and "SPAN_KIND_SERVER" style values.
type flexEnum struct {
	num  int32
	name string
}

func (f *flexEnum) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		f.name = s
		return nil
	}
	var n int32
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("invalid enum %s", string(b))
	}
	f.num = n
	return nil
}

func (f flexEnum) resolve(prefix string, names []string) int32 {
	if f.name == "" {
		return f.num
	}
	n := strings.TrimPrefix(f.name, prefix)
	for i, candidate := range names {
		if n == candidate {
			return int32(i)
		}
	}
	return 0
}

var spanKindNames = []string{"UNSPECIFIED", "INTERNAL", "SERVER", "CLIENT", "PRODUCER", "CONSUMER"}
var statusCodeNames = []string{"UNSET", "OK", "ERROR"}

// hexID validates and normalizes a hex-encoded id of the given byte length.
// Empty and all-zero ids normalize to "" (proto3 zero value semantics).
func hexID(s string, byteLen int, what string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) != byteLen*2 {
		return "", fmt.Errorf("%s must be %d hex chars, got %d", what, byteLen*2, len(s))
	}
	allZero := true
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			if c != '0' {
				allZero = false
			}
		case c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			allZero = false
		default:
			return "", fmt.Errorf("%s is not valid hex: %q", what, s)
		}
	}
	if allZero {
		return "", nil
	}
	return strings.ToLower(s), nil
}

// JSON shapes, mirroring the OTLP/JSON field names.

type jsonAnyValue struct {
	StringValue *string         `json:"stringValue"`
	BoolValue   *bool           `json:"boolValue"`
	IntValue    *flexInt64      `json:"intValue"`
	DoubleValue *float64        `json:"doubleValue"`
	ArrayValue  *jsonArrayValue `json:"arrayValue"`
	KvlistValue *jsonKVList     `json:"kvlistValue"`
	BytesValue  *string         `json:"bytesValue"` // base64 per proto3-JSON
}

type jsonArrayValue struct {
	Values []jsonAnyValue `json:"values"`
}

type jsonKVList struct {
	Values []jsonKeyValue `json:"values"`
}

type jsonKeyValue struct {
	Key   string        `json:"key"`
	Value *jsonAnyValue `json:"value"`
}

type jsonResource struct {
	Attributes []jsonKeyValue `json:"attributes"`
}

type jsonScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type jsonStatus struct {
	Message string   `json:"message"`
	Code    flexEnum `json:"code"`
}

type jsonEvent struct {
	TimeUnixNano flexUint64     `json:"timeUnixNano"`
	Name         string         `json:"name"`
	Attributes   []jsonKeyValue `json:"attributes"`
}

type jsonLink struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	Attributes []jsonKeyValue `json:"attributes"`
}

type jsonSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId"`
	Name              string         `json:"name"`
	Kind              flexEnum       `json:"kind"`
	StartTimeUnixNano flexUint64     `json:"startTimeUnixNano"`
	EndTimeUnixNano   flexUint64     `json:"endTimeUnixNano"`
	Attributes        []jsonKeyValue `json:"attributes"`
	Events            []jsonEvent    `json:"events"`
	Links             []jsonLink     `json:"links"`
	Status            *jsonStatus    `json:"status"`
}

type jsonScopeSpans struct {
	Scope *jsonScope `json:"scope"`
	Spans []jsonSpan `json:"spans"`
}

type jsonResourceSpans struct {
	Resource   *jsonResource    `json:"resource"`
	ScopeSpans []jsonScopeSpans `json:"scopeSpans"`
}

type jsonTraceRequest struct {
	ResourceSpans []jsonResourceSpans `json:"resourceSpans"`
}

type jsonLogRecord struct {
	TimeUnixNano         flexUint64     `json:"timeUnixNano"`
	ObservedTimeUnixNano flexUint64     `json:"observedTimeUnixNano"`
	SeverityNumber       flexEnum       `json:"severityNumber"`
	SeverityText         string         `json:"severityText"`
	Body                 *jsonAnyValue  `json:"body"`
	Attributes           []jsonKeyValue `json:"attributes"`
	TraceID              string         `json:"traceId"`
	SpanID               string         `json:"spanId"`
}

type jsonScopeLogs struct {
	Scope      *jsonScope      `json:"scope"`
	LogRecords []jsonLogRecord `json:"logRecords"`
}

type jsonResourceLogs struct {
	Resource  *jsonResource   `json:"resource"`
	ScopeLogs []jsonScopeLogs `json:"scopeLogs"`
}

type jsonLogsRequest struct {
	ResourceLogs []jsonResourceLogs `json:"resourceLogs"`
}

type jsonMetric struct {
	Name string `json:"name"`
}

type jsonScopeMetrics struct {
	Metrics []jsonMetric `json:"metrics"`
}

type jsonResourceMetrics struct {
	ScopeMetrics []jsonScopeMetrics `json:"scopeMetrics"`
}

type jsonMetricsRequest struct {
	ResourceMetrics []jsonResourceMetrics `json:"resourceMetrics"`
}

// DecodeTracesJSON decodes an OTLP/JSON ExportTraceServiceRequest.
func DecodeTracesJSON(payload []byte) (*TraceData, error) {
	var req jsonTraceRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("traces json: %w", err)
	}
	td := &TraceData{}
	for _, rs := range req.ResourceSpans {
		var res Attrs
		if rs.Resource != nil {
			var err error
			if res, err = convertAttrs(rs.Resource.Attributes); err != nil {
				return nil, err
			}
		}
		service := serviceName(res)
		for _, ss := range rs.ScopeSpans {
			scope := scopeLabel(ss.Scope)
			for _, js := range ss.Spans {
				sp, err := convertSpan(js)
				if err != nil {
					return nil, err
				}
				sp.Service = service
				sp.ResourceAttrs = res
				sp.Scope = scope
				td.Spans = append(td.Spans, sp)
			}
		}
	}
	return td, nil
}

func scopeLabel(s *jsonScope) string {
	if s == nil {
		return ""
	}
	if s.Name != "" && s.Version != "" {
		return s.Name + "@" + s.Version
	}
	return s.Name
}

func convertSpan(js jsonSpan) (Span, error) {
	var sp Span
	var err error
	if sp.TraceID, err = hexID(js.TraceID, 16, "traceId"); err != nil {
		return sp, err
	}
	if sp.SpanID, err = hexID(js.SpanID, 8, "spanId"); err != nil {
		return sp, err
	}
	if sp.ParentSpanID, err = hexID(js.ParentSpanID, 8, "parentSpanId"); err != nil {
		return sp, err
	}
	sp.Name = js.Name
	sp.Kind = SpanKind(js.Kind.resolve("SPAN_KIND_", spanKindNames))
	sp.StartTimeUnixNano = uint64(js.StartTimeUnixNano)
	sp.EndTimeUnixNano = uint64(js.EndTimeUnixNano)
	if sp.Attributes, err = convertAttrs(js.Attributes); err != nil {
		return sp, err
	}
	for _, je := range js.Events {
		attrs, err := convertAttrs(je.Attributes)
		if err != nil {
			return sp, err
		}
		sp.Events = append(sp.Events, Event{
			TimeUnixNano: uint64(je.TimeUnixNano),
			Name:         je.Name,
			Attributes:   attrs,
		})
	}
	for _, jl := range js.Links {
		tid, err := hexID(jl.TraceID, 16, "link traceId")
		if err != nil {
			return sp, err
		}
		sid, err := hexID(jl.SpanID, 8, "link spanId")
		if err != nil {
			return sp, err
		}
		attrs, err := convertAttrs(jl.Attributes)
		if err != nil {
			return sp, err
		}
		sp.Links = append(sp.Links, Link{TraceID: tid, SpanID: sid, Attributes: attrs})
	}
	if js.Status != nil {
		sp.StatusMessage = js.Status.Message
		sp.StatusCode = StatusCode(js.Status.Code.resolve("STATUS_CODE_", statusCodeNames))
	}
	return sp, nil
}

func convertAttrs(in []jsonKeyValue) (Attrs, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(Attrs, 0, len(in))
	for _, kv := range in {
		v := AnyValue{}
		if kv.Value != nil {
			var err error
			if v, err = convertAnyValue(*kv.Value); err != nil {
				return nil, fmt.Errorf("attribute %q: %w", kv.Key, err)
			}
		}
		out = append(out, KeyValue{Key: kv.Key, Value: v})
	}
	return out, nil
}

func convertAnyValue(jv jsonAnyValue) (AnyValue, error) {
	switch {
	case jv.StringValue != nil:
		return AnyValue{Kind: ValueString, Str: *jv.StringValue}, nil
	case jv.BoolValue != nil:
		return AnyValue{Kind: ValueBool, Bool: *jv.BoolValue}, nil
	case jv.IntValue != nil:
		return AnyValue{Kind: ValueInt, Int: int64(*jv.IntValue)}, nil
	case jv.DoubleValue != nil:
		return AnyValue{Kind: ValueDouble, Double: *jv.DoubleValue}, nil
	case jv.ArrayValue != nil:
		var arr []AnyValue
		for _, e := range jv.ArrayValue.Values {
			v, err := convertAnyValue(e)
			if err != nil {
				return AnyValue{}, err
			}
			arr = append(arr, v)
		}
		return AnyValue{Kind: ValueArray, Array: arr}, nil
	case jv.KvlistValue != nil:
		kvs, err := convertAttrs(jv.KvlistValue.Values)
		if err != nil {
			return AnyValue{}, err
		}
		return AnyValue{Kind: ValueKVList, KVList: kvs}, nil
	case jv.BytesValue != nil:
		b, err := base64.StdEncoding.DecodeString(*jv.BytesValue)
		if err != nil {
			return AnyValue{}, fmt.Errorf("bytesValue: %w", err)
		}
		return AnyValue{Kind: ValueBytes, Bytes: b}, nil
	default:
		return AnyValue{}, nil // empty AnyValue is legal
	}
}

// DecodeLogsJSON decodes an OTLP/JSON ExportLogsServiceRequest.
func DecodeLogsJSON(payload []byte) (*LogData, error) {
	var req jsonLogsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("logs json: %w", err)
	}
	ld := &LogData{}
	for _, rl := range req.ResourceLogs {
		var res Attrs
		if rl.Resource != nil {
			var err error
			if res, err = convertAttrs(rl.Resource.Attributes); err != nil {
				return nil, err
			}
		}
		service := serviceName(res)
		for _, sl := range rl.ScopeLogs {
			scope := scopeLabel(sl.Scope)
			for _, jr := range sl.LogRecords {
				rec, err := convertLogRecord(jr)
				if err != nil {
					return nil, err
				}
				rec.Service = service
				rec.ResourceAttrs = res
				rec.Scope = scope
				ld.Records = append(ld.Records, rec)
			}
		}
	}
	return ld, nil
}

func convertLogRecord(jr jsonLogRecord) (LogRecord, error) {
	var rec LogRecord
	rec.TimeUnixNano = uint64(jr.TimeUnixNano)
	if rec.TimeUnixNano == 0 {
		rec.TimeUnixNano = uint64(jr.ObservedTimeUnixNano)
	}
	if jr.SeverityNumber.name != "" {
		// Severity names (SEVERITY_NUMBER_WARN2 etc.) do not map by index
		// like span kinds; derive the number from the band name instead.
		rec.SeverityNumber = severityFromName(jr.SeverityNumber.name)
	} else {
		rec.SeverityNumber = SeverityNumber(jr.SeverityNumber.num)
	}
	rec.SeverityText = jr.SeverityText
	if jr.Body != nil {
		var err error
		if rec.Body, err = convertAnyValue(*jr.Body); err != nil {
			return rec, err
		}
	}
	var err error
	if rec.Attributes, err = convertAttrs(jr.Attributes); err != nil {
		return rec, err
	}
	if rec.TraceID, err = hexID(jr.TraceID, 16, "traceId"); err != nil {
		return rec, err
	}
	if rec.SpanID, err = hexID(jr.SpanID, 8, "spanId"); err != nil {
		return rec, err
	}
	return rec, nil
}

// severityFromName maps SEVERITY_NUMBER_* names to the base number of
// their band (INFO→9, WARN→13, …), enough to derive the right label.
func severityFromName(name string) SeverityNumber {
	n := strings.TrimPrefix(name, "SEVERITY_NUMBER_")
	bands := map[string]SeverityNumber{
		"TRACE": 1, "DEBUG": 5, "INFO": 9, "WARN": 13, "ERROR": 17, "FATAL": 21,
	}
	for band, base := range bands {
		if strings.HasPrefix(n, band) {
			offset := SeverityNumber(0)
			if rest := strings.TrimPrefix(n, band); len(rest) == 1 && rest[0] >= '2' && rest[0] <= '4' {
				offset = SeverityNumber(rest[0] - '1')
			}
			return base + offset
		}
	}
	return 0
}

// SummarizeMetricsJSON shallow-decodes an OTLP/JSON metrics request.
func SummarizeMetricsJSON(payload []byte) (*MetricsSummary, error) {
	var req jsonMetricsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("metrics json: %w", err)
	}
	sum := &MetricsSummary{}
	seen := map[string]bool{}
	for _, rm := range req.ResourceMetrics {
		sum.ResourceCount++
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				sum.MetricCount++
				if m.Name != "" && !seen[m.Name] {
					seen[m.Name] = true
					sum.Names = append(sum.Names, m.Name)
				}
			}
		}
	}
	return sum, nil
}
