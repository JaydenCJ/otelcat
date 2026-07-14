// Decoder tests for the OTLP/JSON encoding, focused on the mapping's
// sharp edges: hex ids, string-or-number 64-bit values, enum names vs
// numbers, and base64 bytes — the exact points where real SDKs diverge.
package otlp

import (
	"strings"
	"testing"
)

func TestDecodeTracesJSONMinimalSpan(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7",
		 "name":"GET /demo","kind":2,"startTimeUnixNano":"1000","endTimeUnixNano":"2500"}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(td.Spans))
	}
	sp := td.Spans[0]
	if sp.TraceID != tid || sp.SpanID != sid || sp.Name != "GET /demo" || sp.Kind != KindServer {
		t.Fatalf("span wrong: %+v", sp)
	}
	if sp.DurationNano() != 1500 {
		t.Fatalf("duration wrong: %d", sp.DurationNano())
	}
}

func TestDecodeTracesJSONTimestampsAsNumbers(t *testing.T) {
	// The spec says string, but several SDKs emit JSON numbers — and
	// some log pipelines even emit floats (1.5e3). All must land on
	// the same value.
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","startTimeUnixNano":1000,"endTimeUnixNano":2500},
		{"spanId":"1a2b3c4d5e6f7081","name":"f","startTimeUnixNano":1.5e3}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if td.Spans[0].StartTimeUnixNano != 1000 || td.Spans[0].EndTimeUnixNano != 2500 {
		t.Fatalf("numeric timestamps wrong: %+v", td.Spans[0])
	}
	if td.Spans[1].StartTimeUnixNano != 1500 {
		t.Fatalf("float timestamp wrong: %d", td.Spans[1].StartTimeUnixNano)
	}
}

func TestDecodeTracesJSONRejectsOutOfRangeTimestamps(t *testing.T) {
	// Floats at or above 2^64 (and negatives) have no uint64 value;
	// converting them would be implementation-defined, so they error.
	for _, bad := range []string{"1e20", "-1"} {
		payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
			{"spanId":"00f067aa0ba902b7","name":"n","startTimeUnixNano":` + bad + `}
		]}]}]}`
		if _, err := DecodeTracesJSON([]byte(payload)); err == nil {
			t.Errorf("timestamp %s should be rejected", bad)
		}
	}
}

func TestDecodeTracesJSONEnumsAsNames(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","kind":"SPAN_KIND_CLIENT",
		 "status":{"code":"STATUS_CODE_ERROR","message":"boom"}}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	sp := td.Spans[0]
	if sp.Kind != KindClient {
		t.Fatalf("named kind wrong: %v", sp.Kind)
	}
	if sp.StatusCode != StatusError || sp.StatusMessage != "boom" {
		t.Fatalf("named status wrong: %v %q", sp.StatusCode, sp.StatusMessage)
	}
}

func TestDecodeTracesJSONIDNormalization(t *testing.T) {
	// Uppercase hex is lowercased; all-zero ids are the proto3 zero
	// value, so a span with a zero parent id is a root, not a child of
	// a phantom "0000…" span.
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"traceId":"4BF92F3577B34DA6A3CE929D0E0E4736","spanId":"00F067AA0BA902B7","name":"upper"},
		{"traceId":"00000000000000000000000000000000","spanId":"00f067aa0ba902b7",
		 "parentSpanId":"0000000000000000","name":"zeros"}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if td.Spans[0].TraceID != tid || td.Spans[0].SpanID != sid {
		t.Fatalf("hex not lowercased: %+v", td.Spans[0])
	}
	if td.Spans[1].TraceID != "" || td.Spans[1].ParentSpanID != "" {
		t.Fatalf("zero ids should normalize to empty: %+v", td.Spans[1])
	}
}

func TestDecodeTracesJSONBadHexRejected(t *testing.T) {
	for _, id := range []string{"zzf92f3577b34da6a3ce929d0e0e4736", "abc"} {
		payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"` + id + `","name":"n"}]}]}]}`
		if _, err := DecodeTracesJSON([]byte(payload)); err == nil {
			t.Errorf("traceId %q should be rejected", id)
		}
	}
}

func TestDecodeTracesJSONAttributeTypes(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","attributes":[
			{"key":"s","value":{"stringValue":"x"}},
			{"key":"b","value":{"boolValue":true}},
			{"key":"i","value":{"intValue":"42"}},
			{"key":"inum","value":{"intValue":7}},
			{"key":"d","value":{"doubleValue":2.5}},
			{"key":"raw","value":{"bytesValue":"3q0="}},
			{"key":"arr","value":{"arrayValue":{"values":[{"stringValue":"a"},{"intValue":"2"}]}}},
			{"key":"map","value":{"kvlistValue":{"values":[{"key":"inner","value":{"stringValue":"y"}}]}}}
		]}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	attrs := td.Spans[0].Attributes
	want := map[string]string{
		"s": "x", "b": "true", "i": "42", "inum": "7", "d": "2.5",
		"raw": "0xdead", "arr": "[a, 2]", "map": "{inner=y}",
	}
	for key, expect := range want {
		v, ok := attrs.Get(key)
		if !ok || v.String() != expect {
			t.Errorf("attr %q: want %q, got %q (present=%v)", key, expect, v.String(), ok)
		}
	}
}

func TestDecodeTracesJSONEventsAndLinks(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","startTimeUnixNano":"100",
		 "events":[{"timeUnixNano":"150","name":"exception",
		            "attributes":[{"key":"exception.type","value":{"stringValue":"Boom"}}]}],
		 "links":[{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"1a2b3c4d5e6f7081"}]}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	sp := td.Spans[0]
	if len(sp.Events) != 1 || sp.Events[0].TimeUnixNano != 150 {
		t.Fatalf("events wrong: %+v", sp.Events)
	}
	if len(sp.Links) != 1 || sp.Links[0].SpanID != pid {
		t.Fatalf("links wrong: %+v", sp.Links)
	}
}

func TestDecodeTracesJSONResourceAndScope(t *testing.T) {
	payload := `{"resourceSpans":[{
		"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"api"}}]},
		"scopeSpans":[{"scope":{"name":"lib","version":"3.0"},"spans":[{"spanId":"00f067aa0ba902b7","name":"n"}]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if td.Spans[0].Service != "api" || td.Spans[0].Scope != "lib@3.0" {
		t.Fatalf("context wrong: %q %q", td.Spans[0].Service, td.Spans[0].Scope)
	}
}

func TestDecodeTracesJSONMalformedRejected(t *testing.T) {
	for _, payload := range []string{"", "{", `[]`, `{"resourceSpans":42}`} {
		if _, err := DecodeTracesJSON([]byte(payload)); err == nil {
			t.Errorf("payload %q should be rejected", payload)
		}
	}
}

func TestDecodeTracesJSONUnknownFieldsIgnored(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","droppedAttributesCount":3,"flags":256,"futureField":{"x":1}}
	]}]}]}`
	td, err := DecodeTracesJSON([]byte(payload))
	if err != nil || td.Spans[0].Name != "n" {
		t.Fatalf("unknown JSON fields must be ignored: %v", err)
	}
}

func TestDecodeLogsJSONFullRecord(t *testing.T) {
	payload := `{"resourceLogs":[{
		"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"worker"}}]},
		"scopeLogs":[{"logRecords":[
			{"timeUnixNano":"5000","severityNumber":13,"severityText":"","body":{"stringValue":"queue full"},
			 "attributes":[{"key":"queue","value":{"stringValue":"jobs"}}],
			 "traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7"}
		]}]}]}`
	ld, err := DecodeLogsJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	rec := ld.Records[0]
	if rec.Level() != "WARN" {
		t.Fatalf("severity 13 should be WARN band, got %q", rec.Level())
	}
	if rec.Service != "worker" || rec.Body.String() != "queue full" {
		t.Fatalf("record wrong: %+v", rec)
	}
	if rec.TraceID != tid {
		t.Fatalf("trace correlation lost: %q", rec.TraceID)
	}
}

func TestDecodeLogsJSONLenientFields(t *testing.T) {
	// Two leniencies real pipelines need: severity as an enum *name*
	// (which does not map by index like span kinds), and records that
	// carry only observedTimeUnixNano.
	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
		{"severityNumber":"SEVERITY_NUMBER_ERROR2","body":{"stringValue":"x"}},
		{"observedTimeUnixNano":"7777","body":{"stringValue":"y"}}
	]}]}]}`
	ld, err := DecodeLogsJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if ld.Records[0].Level() != "ERROR" {
		t.Fatalf("SEVERITY_NUMBER_ERROR2 should map to ERROR band, got %q", ld.Records[0].Level())
	}
	if ld.Records[1].TimeUnixNano != 7777 {
		t.Fatalf("observedTimeUnixNano fallback missing: %d", ld.Records[1].TimeUnixNano)
	}
}

func TestSummarizeMetricsJSON(t *testing.T) {
	payload := `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[
		{"name":"a"},{"name":"b"},{"name":"a"}
	]}]}]}`
	sum, err := SummarizeMetricsJSON([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if sum.ResourceCount != 1 || sum.MetricCount != 3 || len(sum.Names) != 2 {
		t.Fatalf("summary wrong: %+v", sum)
	}
}

func TestFlexInt64RejectsNonNumeric(t *testing.T) {
	payload := `{"resourceSpans":[{"scopeSpans":[{"spans":[
		{"spanId":"00f067aa0ba902b7","name":"n","attributes":[{"key":"i","value":{"intValue":"forty-two"}}]}
	]}]}]}`
	_, err := DecodeTracesJSON([]byte(payload))
	if err == nil || !strings.Contains(err.Error(), "int64") {
		t.Fatalf("non-numeric intValue must fail with a clear error, got %v", err)
	}
}
