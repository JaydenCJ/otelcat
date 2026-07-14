// Protobuf decoding for OTLP/HTTP with Content-Type application/x-protobuf.
// Field numbers are transcribed from opentelemetry-proto v1.x (trace/v1,
// logs/v1, metrics/v1, common/v1, resource/v1); unknown fields are skipped
// so payloads from newer SDKs still decode.
package otlp

import (
	"encoding/hex"
	"fmt"

	"github.com/JaydenCJ/otelcat/internal/protowire"
)

// DecodeTracesProto decodes a serialized ExportTraceServiceRequest.
func DecodeTracesProto(payload []byte) (*TraceData, error) {
	td := &TraceData{}
	d := protowire.New(payload)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, fmt.Errorf("traces request: %w", err)
		}
		if field == 1 && wt == protowire.TypeBytes { // resource_spans
			b, err := d.Bytes()
			if err != nil {
				return nil, fmt.Errorf("resource_spans: %w", err)
			}
			if err := decodeResourceSpans(b, td); err != nil {
				return nil, err
			}
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, fmt.Errorf("traces request field %d: %w", field, err)
		}
	}
	return td, nil
}

func decodeResourceSpans(buf []byte, td *TraceData) error {
	var res Attrs
	var scopeBlocks [][]byte
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return fmt.Errorf("resource_spans: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes: // resource
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			if res, err = decodeResource(b); err != nil {
				return err
			}
		case field == 2 && wt == protowire.TypeBytes: // scope_spans
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			scopeBlocks = append(scopeBlocks, b)
		default:
			if err := d.Skip(wt); err != nil {
				return err
			}
		}
	}
	service := serviceName(res)
	for _, b := range scopeBlocks {
		if err := decodeScopeSpans(b, res, service, td); err != nil {
			return err
		}
	}
	return nil
}

func decodeScopeSpans(buf []byte, res Attrs, service string, td *TraceData) error {
	var scope string
	var spanBlocks [][]byte
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return fmt.Errorf("scope_spans: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes: // scope
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			if scope, err = decodeScope(b); err != nil {
				return err
			}
		case field == 2 && wt == protowire.TypeBytes: // spans
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			spanBlocks = append(spanBlocks, b)
		default:
			if err := d.Skip(wt); err != nil {
				return err
			}
		}
	}
	for _, b := range spanBlocks {
		sp, err := decodeSpan(b)
		if err != nil {
			return err
		}
		sp.Service = service
		sp.ResourceAttrs = res
		sp.Scope = scope
		td.Spans = append(td.Spans, sp)
	}
	return nil
}

func decodeSpan(buf []byte) (Span, error) {
	var sp Span
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return sp, fmt.Errorf("span: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes: // trace_id
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			sp.TraceID = hex.EncodeToString(b)
		case field == 2 && wt == protowire.TypeBytes: // span_id
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			sp.SpanID = hex.EncodeToString(b)
		case field == 4 && wt == protowire.TypeBytes: // parent_span_id
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			sp.ParentSpanID = hex.EncodeToString(b)
		case field == 5 && wt == protowire.TypeBytes: // name
			if sp.Name, err = d.String(); err != nil {
				return sp, err
			}
		case field == 6 && wt == protowire.TypeVarint: // kind
			v, err := d.Varint()
			if err != nil {
				return sp, err
			}
			sp.Kind = SpanKind(v)
		case field == 7 && wt == protowire.TypeFixed64: // start_time_unix_nano
			if sp.StartTimeUnixNano, err = d.Fixed64(); err != nil {
				return sp, err
			}
		case field == 8 && wt == protowire.TypeFixed64: // end_time_unix_nano
			if sp.EndTimeUnixNano, err = d.Fixed64(); err != nil {
				return sp, err
			}
		case field == 9 && wt == protowire.TypeBytes: // attributes
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return sp, err
			}
			sp.Attributes = append(sp.Attributes, kv)
		case field == 11 && wt == protowire.TypeBytes: // events
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			ev, err := decodeEvent(b)
			if err != nil {
				return sp, err
			}
			sp.Events = append(sp.Events, ev)
		case field == 13 && wt == protowire.TypeBytes: // links
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			ln, err := decodeLink(b)
			if err != nil {
				return sp, err
			}
			sp.Links = append(sp.Links, ln)
		case field == 15 && wt == protowire.TypeBytes: // status
			b, err := d.Bytes()
			if err != nil {
				return sp, err
			}
			if err := decodeStatus(b, &sp); err != nil {
				return sp, err
			}
		default:
			if err := d.Skip(wt); err != nil {
				return sp, fmt.Errorf("span field %d: %w", field, err)
			}
		}
	}
	return sp, nil
}

func decodeEvent(buf []byte) (Event, error) {
	var ev Event
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return ev, fmt.Errorf("event: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeFixed64:
			if ev.TimeUnixNano, err = d.Fixed64(); err != nil {
				return ev, err
			}
		case field == 2 && wt == protowire.TypeBytes:
			if ev.Name, err = d.String(); err != nil {
				return ev, err
			}
		case field == 3 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return ev, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return ev, err
			}
			ev.Attributes = append(ev.Attributes, kv)
		default:
			if err := d.Skip(wt); err != nil {
				return ev, err
			}
		}
	}
	return ev, nil
}

func decodeLink(buf []byte) (Link, error) {
	var ln Link
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return ln, fmt.Errorf("link: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return ln, err
			}
			ln.TraceID = hex.EncodeToString(b)
		case field == 2 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return ln, err
			}
			ln.SpanID = hex.EncodeToString(b)
		case field == 4 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return ln, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return ln, err
			}
			ln.Attributes = append(ln.Attributes, kv)
		default:
			if err := d.Skip(wt); err != nil {
				return ln, err
			}
		}
	}
	return ln, nil
}

func decodeStatus(buf []byte, sp *Span) error {
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return fmt.Errorf("status: %w", err)
		}
		switch {
		case field == 2 && wt == protowire.TypeBytes: // message
			if sp.StatusMessage, err = d.String(); err != nil {
				return err
			}
		case field == 3 && wt == protowire.TypeVarint: // code
			v, err := d.Varint()
			if err != nil {
				return err
			}
			sp.StatusCode = StatusCode(v)
		default:
			if err := d.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeResource(buf []byte) (Attrs, error) {
	var attrs Attrs
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, fmt.Errorf("resource: %w", err)
		}
		if field == 1 && wt == protowire.TypeBytes { // attributes
			b, err := d.Bytes()
			if err != nil {
				return nil, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return nil, err
			}
			attrs = append(attrs, kv)
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, err
		}
	}
	return attrs, nil
}

func decodeScope(buf []byte) (string, error) {
	var name, ver string
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return "", fmt.Errorf("scope: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes:
			if name, err = d.String(); err != nil {
				return "", err
			}
		case field == 2 && wt == protowire.TypeBytes:
			if ver, err = d.String(); err != nil {
				return "", err
			}
		default:
			if err := d.Skip(wt); err != nil {
				return "", err
			}
		}
	}
	if name != "" && ver != "" {
		return name + "@" + ver, nil
	}
	return name, nil
}

func decodeKeyValue(buf []byte) (KeyValue, error) {
	var kv KeyValue
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return kv, fmt.Errorf("key_value: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes:
			if kv.Key, err = d.String(); err != nil {
				return kv, err
			}
		case field == 2 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return kv, err
			}
			if kv.Value, err = decodeAnyValue(b); err != nil {
				return kv, err
			}
		default:
			if err := d.Skip(wt); err != nil {
				return kv, err
			}
		}
	}
	return kv, nil
}

func decodeAnyValue(buf []byte) (AnyValue, error) {
	var v AnyValue
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return v, fmt.Errorf("any_value: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes: // string_value
			v.Kind = ValueString
			if v.Str, err = d.String(); err != nil {
				return v, err
			}
		case field == 2 && wt == protowire.TypeVarint: // bool_value
			n, err := d.Varint()
			if err != nil {
				return v, err
			}
			v.Kind, v.Bool = ValueBool, n != 0
		case field == 3 && wt == protowire.TypeVarint: // int_value
			n, err := d.Varint()
			if err != nil {
				return v, err
			}
			v.Kind, v.Int = ValueInt, int64(n)
		case field == 4 && wt == protowire.TypeFixed64: // double_value
			if v.Double, err = d.Double(); err != nil {
				return v, err
			}
			v.Kind = ValueDouble
		case field == 5 && wt == protowire.TypeBytes: // array_value
			b, err := d.Bytes()
			if err != nil {
				return v, err
			}
			arr, err := decodeValueList(b)
			if err != nil {
				return v, err
			}
			v.Kind, v.Array = ValueArray, arr
		case field == 6 && wt == protowire.TypeBytes: // kvlist_value
			b, err := d.Bytes()
			if err != nil {
				return v, err
			}
			kvs, err := decodeKVList(b)
			if err != nil {
				return v, err
			}
			v.Kind, v.KVList = ValueKVList, kvs
		case field == 7 && wt == protowire.TypeBytes: // bytes_value
			b, err := d.Bytes()
			if err != nil {
				return v, err
			}
			v.Kind = ValueBytes
			v.Bytes = append([]byte(nil), b...)
		default:
			if err := d.Skip(wt); err != nil {
				return v, err
			}
		}
	}
	return v, nil
}

func decodeValueList(buf []byte) ([]AnyValue, error) {
	var out []AnyValue
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, err
		}
		if field == 1 && wt == protowire.TypeBytes {
			b, err := d.Bytes()
			if err != nil {
				return nil, err
			}
			v, err := decodeAnyValue(b)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeKVList(buf []byte) ([]KeyValue, error) {
	var out []KeyValue
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, err
		}
		if field == 1 && wt == protowire.TypeBytes {
			b, err := d.Bytes()
			if err != nil {
				return nil, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return nil, err
			}
			out = append(out, kv)
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func serviceName(res Attrs) string {
	if v, ok := res.Get("service.name"); ok && v.Kind == ValueString {
		return v.Str
	}
	return ""
}

// DecodeLogsProto decodes a serialized ExportLogsServiceRequest.
func DecodeLogsProto(payload []byte) (*LogData, error) {
	ld := &LogData{}
	d := protowire.New(payload)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, fmt.Errorf("logs request: %w", err)
		}
		if field == 1 && wt == protowire.TypeBytes { // resource_logs
			b, err := d.Bytes()
			if err != nil {
				return nil, err
			}
			if err := decodeResourceLogs(b, ld); err != nil {
				return nil, err
			}
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, err
		}
	}
	return ld, nil
}

func decodeResourceLogs(buf []byte, ld *LogData) error {
	var res Attrs
	var scopeBlocks [][]byte
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return fmt.Errorf("resource_logs: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			if res, err = decodeResource(b); err != nil {
				return err
			}
		case field == 2 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			scopeBlocks = append(scopeBlocks, b)
		default:
			if err := d.Skip(wt); err != nil {
				return err
			}
		}
	}
	service := serviceName(res)
	for _, b := range scopeBlocks {
		if err := decodeScopeLogs(b, res, service, ld); err != nil {
			return err
		}
	}
	return nil
}

func decodeScopeLogs(buf []byte, res Attrs, service string, ld *LogData) error {
	var scope string
	var recBlocks [][]byte
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return fmt.Errorf("scope_logs: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			if scope, err = decodeScope(b); err != nil {
				return err
			}
		case field == 2 && wt == protowire.TypeBytes:
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			recBlocks = append(recBlocks, b)
		default:
			if err := d.Skip(wt); err != nil {
				return err
			}
		}
	}
	for _, b := range recBlocks {
		rec, err := decodeLogRecord(b)
		if err != nil {
			return err
		}
		rec.Service = service
		rec.ResourceAttrs = res
		rec.Scope = scope
		ld.Records = append(ld.Records, rec)
	}
	return nil
}

func decodeLogRecord(buf []byte) (LogRecord, error) {
	var rec LogRecord
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return rec, fmt.Errorf("log_record: %w", err)
		}
		switch {
		case field == 1 && wt == protowire.TypeFixed64: // time_unix_nano
			if rec.TimeUnixNano, err = d.Fixed64(); err != nil {
				return rec, err
			}
		case field == 2 && wt == protowire.TypeVarint: // severity_number
			v, err := d.Varint()
			if err != nil {
				return rec, err
			}
			rec.SeverityNumber = SeverityNumber(v)
		case field == 3 && wt == protowire.TypeBytes: // severity_text
			if rec.SeverityText, err = d.String(); err != nil {
				return rec, err
			}
		case field == 5 && wt == protowire.TypeBytes: // body
			b, err := d.Bytes()
			if err != nil {
				return rec, err
			}
			if rec.Body, err = decodeAnyValue(b); err != nil {
				return rec, err
			}
		case field == 6 && wt == protowire.TypeBytes: // attributes
			b, err := d.Bytes()
			if err != nil {
				return rec, err
			}
			kv, err := decodeKeyValue(b)
			if err != nil {
				return rec, err
			}
			rec.Attributes = append(rec.Attributes, kv)
		case field == 9 && wt == protowire.TypeBytes: // trace_id
			b, err := d.Bytes()
			if err != nil {
				return rec, err
			}
			rec.TraceID = hex.EncodeToString(b)
		case field == 10 && wt == protowire.TypeBytes: // span_id
			b, err := d.Bytes()
			if err != nil {
				return rec, err
			}
			rec.SpanID = hex.EncodeToString(b)
		case field == 11 && wt == protowire.TypeFixed64: // observed_time_unix_nano
			t, err := d.Fixed64()
			if err != nil {
				return rec, err
			}
			if rec.TimeUnixNano == 0 {
				rec.TimeUnixNano = t
			}
		default:
			if err := d.Skip(wt); err != nil {
				return rec, err
			}
		}
	}
	return rec, nil
}

// SummarizeMetricsProto shallow-decodes an ExportMetricsServiceRequest,
// counting resources and collecting metric names. Full metric rendering
// is a roadmap item; otelcat still acknowledges the payload so SDK metric
// exporters do not error out.
func SummarizeMetricsProto(payload []byte) (*MetricsSummary, error) {
	sum := &MetricsSummary{}
	seen := map[string]bool{}
	d := protowire.New(payload)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return nil, fmt.Errorf("metrics request: %w", err)
		}
		if field == 1 && wt == protowire.TypeBytes { // resource_metrics
			b, err := d.Bytes()
			if err != nil {
				return nil, err
			}
			sum.ResourceCount++
			if err := summarizeResourceMetrics(b, sum, seen); err != nil {
				return nil, err
			}
			continue
		}
		if err := d.Skip(wt); err != nil {
			return nil, err
		}
	}
	return sum, nil
}

func summarizeResourceMetrics(buf []byte, sum *MetricsSummary, seen map[string]bool) error {
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return err
		}
		if field == 2 && wt == protowire.TypeBytes { // scope_metrics
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			if err := summarizeScopeMetrics(b, sum, seen); err != nil {
				return err
			}
			continue
		}
		if err := d.Skip(wt); err != nil {
			return err
		}
	}
	return nil
}

func summarizeScopeMetrics(buf []byte, sum *MetricsSummary, seen map[string]bool) error {
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return err
		}
		if field == 2 && wt == protowire.TypeBytes { // metrics
			b, err := d.Bytes()
			if err != nil {
				return err
			}
			sum.MetricCount++
			name, err := metricName(b)
			if err != nil {
				return err
			}
			if name != "" && !seen[name] {
				seen[name] = true
				sum.Names = append(sum.Names, name)
			}
			continue
		}
		if err := d.Skip(wt); err != nil {
			return err
		}
	}
	return nil
}

func metricName(buf []byte) (string, error) {
	d := protowire.New(buf)
	for !d.Done() {
		field, wt, err := d.Tag()
		if err != nil {
			return "", err
		}
		if field == 1 && wt == protowire.TypeBytes { // name
			return d.String()
		}
		if err := d.Skip(wt); err != nil {
			return "", err
		}
	}
	return "", nil
}
