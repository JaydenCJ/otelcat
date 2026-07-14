// Decoder tests for the protobuf encoding of OTLP. Payloads are built
// with the test-only encoder in encoder_test.go so every byte the
// decoder sees was produced by the actual wire rules.
package otlp

import (
	"strings"
	"testing"
)

const (
	tid = "4bf92f3577b34da6a3ce929d0e0e4736"
	sid = "00f067aa0ba902b7"
	pid = "1a2b3c4d5e6f7081"
)

// minimalSpan encodes one span with the given extra fields.
func minimalSpan(t *testing.T, extra func(*enc)) []byte {
	t.Helper()
	return spanRequest(nil, nil, func(ss *enc) {
		ss.msg(2, func(sp *enc) {
			sp.blob(1, mustHex(t, tid))
			sp.blob(2, mustHex(t, sid))
			sp.str(5, "GET /demo")
			sp.uvarint(6, 2) // SERVER
			sp.fixed64(7, 1_000)
			sp.fixed64(8, 2_500)
			if extra != nil {
				extra(sp)
			}
		})
	})
}

func TestDecodeTracesProtoMinimalSpan(t *testing.T) {
	td, err := DecodeTracesProto(minimalSpan(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(td.Spans))
	}
	sp := td.Spans[0]
	if sp.TraceID != tid || sp.SpanID != sid {
		t.Fatalf("ids not hex-normalized: %q %q", sp.TraceID, sp.SpanID)
	}
	if sp.Name != "GET /demo" || sp.Kind != KindServer {
		t.Fatalf("name/kind wrong: %q %v", sp.Name, sp.Kind)
	}
	if sp.StartTimeUnixNano != 1_000 || sp.EndTimeUnixNano != 2_500 {
		t.Fatalf("timestamps wrong: %d %d", sp.StartTimeUnixNano, sp.EndTimeUnixNano)
	}
	if sp.DurationNano() != 1_500 {
		t.Fatalf("duration wrong: %d", sp.DurationNano())
	}
}

func TestDecodeTracesProtoEmptyRequest(t *testing.T) {
	td, err := DecodeTracesProto(nil)
	if err != nil || len(td.Spans) != 0 {
		t.Fatalf("empty request should decode to zero spans: %v", err)
	}
}

func TestDecodeTracesProtoParentAndStatus(t *testing.T) {
	payload := minimalSpan(t, func(sp *enc) {
		sp.blob(4, mustHex(t, pid))
		sp.msg(15, func(st *enc) {
			st.str(2, "card declined")
			st.uvarint(3, 2)
		})
	})
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	sp := td.Spans[0]
	if sp.ParentSpanID != pid {
		t.Fatalf("parent wrong: %q", sp.ParentSpanID)
	}
	if sp.StatusCode != StatusError || sp.StatusMessage != "card declined" {
		t.Fatalf("status wrong: %v %q", sp.StatusCode, sp.StatusMessage)
	}
}

func TestDecodeTracesProtoAllAttributeTypes(t *testing.T) {
	payload := minimalSpan(t, func(sp *enc) {
		strAttr(sp, 9, "s", "hello")
		sp.msg(9, func(kv *enc) { // bool
			kv.str(1, "b")
			kv.msg(2, func(v *enc) { v.uvarint(2, 1) })
		})
		sp.msg(9, func(kv *enc) { // int
			kv.str(1, "i")
			kv.msg(2, func(v *enc) { v.uvarint(3, 42) })
		})
		sp.msg(9, func(kv *enc) { // double 12.5
			kv.str(1, "d")
			kv.msg(2, func(v *enc) { v.fixed64(4, 0x4029000000000000) })
		})
		sp.msg(9, func(kv *enc) { // bytes
			kv.str(1, "raw")
			kv.msg(2, func(v *enc) { v.blob(7, []byte{0xDE, 0xAD}) })
		})
	})
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	attrs := td.Spans[0].Attributes
	want := map[string]string{"s": "hello", "b": "true", "i": "42", "d": "12.5", "raw": "0xdead"}
	if len(attrs) != len(want) {
		t.Fatalf("want %d attrs, got %d", len(want), len(attrs))
	}
	for _, kv := range attrs {
		if got := kv.Value.String(); got != want[kv.Key] {
			t.Errorf("attr %q: want %q, got %q", kv.Key, want[kv.Key], got)
		}
	}
}

func TestDecodeTracesProtoNestedArrayAndKVList(t *testing.T) {
	payload := minimalSpan(t, func(sp *enc) {
		sp.msg(9, func(kv *enc) {
			kv.str(1, "arr")
			kv.msg(2, func(v *enc) {
				v.msg(5, func(av *enc) { // ArrayValue
					av.msg(1, func(e *enc) { e.str(1, "a") })
					av.msg(1, func(e *enc) { e.uvarint(3, 2) })
				})
			})
		})
		sp.msg(9, func(kv *enc) {
			kv.str(1, "map")
			kv.msg(2, func(v *enc) {
				v.msg(6, func(kl *enc) { // KeyValueList
					strAttr(kl, 1, "inner", "x")
				})
			})
		})
	})
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	attrs := td.Spans[0].Attributes
	if got, _ := attrs.Get("arr"); got.String() != "[a, 2]" {
		t.Fatalf("array wrong: %q", got.String())
	}
	if got, _ := attrs.Get("map"); got.String() != "{inner=x}" {
		t.Fatalf("kvlist wrong: %q", got.String())
	}
}

func TestDecodeTracesProtoEventsAndLinks(t *testing.T) {
	payload := minimalSpan(t, func(sp *enc) {
		sp.msg(11, func(ev *enc) {
			ev.fixed64(1, 1_800)
			ev.str(2, "exception")
			strAttr(ev, 3, "exception.type", "Boom")
		})
		sp.msg(13, func(ln *enc) {
			ln.blob(1, mustHex(t, tid))
			ln.blob(2, mustHex(t, pid))
		})
	})
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	sp := td.Spans[0]
	if len(sp.Events) != 1 || sp.Events[0].Name != "exception" || sp.Events[0].TimeUnixNano != 1_800 {
		t.Fatalf("event wrong: %+v", sp.Events)
	}
	if v, ok := sp.Events[0].Attributes.Get("exception.type"); !ok || v.Str != "Boom" {
		t.Fatalf("event attr wrong: %+v", sp.Events[0].Attributes)
	}
	if len(sp.Links) != 1 || sp.Links[0].TraceID != tid || sp.Links[0].SpanID != pid {
		t.Fatalf("link wrong: %+v", sp.Links)
	}
}

func TestDecodeTracesProtoResourceAndScopeContext(t *testing.T) {
	payload := spanRequest(
		func(r *enc) {
			strAttr(r, 1, "service.name", "checkout")
			strAttr(r, 1, "host.name", "dev-box")
		},
		func(sc *enc) {
			sc.str(1, "demo-lib")
			sc.str(2, "2.1.0")
		},
		func(ss *enc) {
			ss.msg(2, func(sp *enc) { sp.str(5, "op") })
		},
	)
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	sp := td.Spans[0]
	if sp.Service != "checkout" {
		t.Fatalf("service not lifted from resource: %q", sp.Service)
	}
	if sp.Scope != "demo-lib@2.1.0" {
		t.Fatalf("scope label wrong: %q", sp.Scope)
	}
	if v, ok := sp.ResourceAttrs.Get("host.name"); !ok || v.Str != "dev-box" {
		t.Fatalf("resource attrs not attached: %+v", sp.ResourceAttrs)
	}
}

func TestDecodeTracesProtoMultipleResourceGroups(t *testing.T) {
	// Two ResourceSpans blocks (two services in one batch) — the shape
	// a Collector-less multi-service test emits when SDKs share a sink.
	var e enc
	for _, svc := range []string{"frontend", "backend"} {
		svc := svc
		e.msg(1, func(rs *enc) {
			rs.msg(1, func(r *enc) { strAttr(r, 1, "service.name", svc) })
			rs.msg(2, func(ss *enc) {
				ss.msg(2, func(sp *enc) { sp.str(5, svc+"-op") })
			})
		})
	}
	td, err := DecodeTracesProto(e.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(td.Spans))
	}
	if td.Spans[0].Service != "frontend" || td.Spans[1].Service != "backend" {
		t.Fatalf("services wrong: %q %q", td.Spans[0].Service, td.Spans[1].Service)
	}
}

func TestDecodeTracesProtoSkipsUnknownFields(t *testing.T) {
	// A span carrying fields otelcat does not know (trace_state,
	// dropped counts, flags, and a hypothetical future field 99) must
	// decode cleanly — SDKs newer than the decoder are the common case.
	payload := minimalSpan(t, func(sp *enc) {
		sp.str(3, "vendor=state") // trace_state
		sp.uvarint(10, 4)         // dropped_attributes_count
		sp.tag(16, 5)             // flags, fixed32
		sp.b = append(sp.b, 1, 0, 0, 0)
		sp.str(99, "from the future")
	})
	td, err := DecodeTracesProto(payload)
	if err != nil {
		t.Fatal(err)
	}
	if td.Spans[0].Name != "GET /demo" {
		t.Fatalf("known fields lost while skipping unknown ones: %+v", td.Spans[0])
	}
}

func TestDecodeTracesProtoTruncatedPayloadFails(t *testing.T) {
	full := minimalSpan(t, nil)
	_, err := DecodeTracesProto(full[:len(full)-3])
	if err == nil {
		t.Fatal("truncated payload must not decode silently")
	}
	// The error must say what went wrong: a platform engineer debugging
	// a hand-rolled exporter reads this message.
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("error should mention truncation: %v", err)
	}
}

func TestDecodeTracesProtoRejectsGarbage(t *testing.T) {
	if _, err := DecodeTracesProto([]byte("this is not protobuf at all")); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestDecodeLogsProtoFullRecord(t *testing.T) {
	var e enc
	e.msg(1, func(rl *enc) {
		rl.msg(1, func(r *enc) { strAttr(r, 1, "service.name", "checkout") })
		rl.msg(2, func(sl *enc) {
			sl.msg(1, func(sc *enc) { sc.str(1, "applog") })
			sl.msg(2, func(lr *enc) {
				lr.fixed64(1, 5_000)
				lr.uvarint(2, 17) // ERROR
				lr.str(3, "ERROR")
				lr.msg(5, func(b *enc) { b.str(1, "payment failed") })
				strAttr(lr, 6, "order.id", "8123")
				lr.blob(9, mustHex(t, tid))
				lr.blob(10, mustHex(t, sid))
			})
		})
	})
	ld, err := DecodeLogsProto(e.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(ld.Records) != 1 {
		t.Fatalf("want 1 record, got %d", len(ld.Records))
	}
	rec := ld.Records[0]
	if rec.Body.String() != "payment failed" || rec.Level() != "ERROR" {
		t.Fatalf("body/level wrong: %q %q", rec.Body.String(), rec.Level())
	}
	if rec.Service != "checkout" || rec.Scope != "applog" {
		t.Fatalf("context wrong: %q %q", rec.Service, rec.Scope)
	}
	if rec.TraceID != tid || rec.SpanID != sid {
		t.Fatalf("correlation ids wrong: %q %q", rec.TraceID, rec.SpanID)
	}
}

func TestDecodeLogsProtoObservedTimeFallback(t *testing.T) {
	// Records from log appenders often carry only observed_time_unix_nano
	// (field 11); the renderer needs *some* timestamp, so it falls back.
	var e enc
	e.msg(1, func(rl *enc) {
		rl.msg(2, func(sl *enc) {
			sl.msg(2, func(lr *enc) {
				lr.fixed64(11, 9_999)
				lr.msg(5, func(b *enc) { b.str(1, "hello") })
			})
		})
	})
	ld, err := DecodeLogsProto(e.b)
	if err != nil {
		t.Fatal(err)
	}
	if ld.Records[0].TimeUnixNano != 9_999 {
		t.Fatalf("observed time not used: %d", ld.Records[0].TimeUnixNano)
	}
}

func TestSummarizeMetricsProtoCountsAndNames(t *testing.T) {
	var e enc
	for i := 0; i < 2; i++ {
		e.msg(1, func(rm *enc) {
			rm.msg(2, func(sm *enc) {
				sm.msg(2, func(m *enc) { m.str(1, "http.server.request.duration") })
				sm.msg(2, func(m *enc) { m.str(1, "queue.depth") })
			})
		})
	}
	sum, err := SummarizeMetricsProto(e.b)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ResourceCount != 2 || sum.MetricCount != 4 {
		t.Fatalf("counts wrong: %+v", sum)
	}
	// Duplicate names collapse, order of first appearance preserved.
	if len(sum.Names) != 2 || sum.Names[0] != "http.server.request.duration" {
		t.Fatalf("names wrong: %v", sum.Names)
	}
}

func TestEnumNamesAndSeverityBands(t *testing.T) {
	if KindServer.String() != "SERVER" || KindConsumer.String() != "CONSUMER" ||
		SpanKind(42).String() != "UNSPECIFIED" {
		t.Fatal("span kind names wrong")
	}
	if StatusOK.String() != "OK" || StatusError.String() != "ERROR" || StatusUnset.String() != "UNSET" {
		t.Fatal("status names wrong")
	}
	bands := map[SeverityNumber]string{
		0: "UNSET", 1: "TRACE", 5: "DEBUG", 9: "INFO", 12: "INFO",
		13: "WARN", 17: "ERROR", 21: "FATAL", 24: "FATAL", 25: "UNSET",
	}
	for n, want := range bands {
		if got := n.SeverityText(); got != want {
			t.Errorf("severity %d: want %s, got %s", n, want, got)
		}
	}
}

func TestModelEdgeCases(t *testing.T) {
	// A misbehaving SDK with end < start must render as 0, not as a
	// wrapped-around uint64 (~584 years).
	sp := Span{StartTimeUnixNano: 100, EndTimeUnixNano: 50}
	if sp.DurationNano() != 0 {
		t.Fatalf("inverted timestamps: want 0, got %d", sp.DurationNano())
	}
	// An empty AnyValue (legal in OTLP) stringifies to empty, not "<nil>".
	var v AnyValue
	if v.String() != "" {
		t.Fatalf("empty AnyValue should stringify to empty, got %q", v.String())
	}
}
