// Command sendspan posts a small, deterministic demo payload to an
// otelcat endpoint — a four-span checkout trace with an error and an
// exception event, two log records, and one metrics batch. It doubles
// as a reference for the OTLP/HTTP request shape in both encodings:
// the protobuf branch hand-encodes the wire format so you can see
// exactly which bytes an SDK exporter produces.
//
// Usage:
//
//	go run ./examples/sendspan --endpoint http://127.0.0.1:4318 --encoding json
//	go run ./examples/sendspan --encoding protobuf
package main

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// A fixed base time keeps every run byte-identical, so the same command
// that demos otelcat also smoke-tests it.
const baseNano = uint64(1767366245000000000) // 2026-01-02T15:04:05Z

const (
	traceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
	rootID   = "00f067aa0ba902b7"
	cartID   = "1a2b3c4d5e6f7081"
	dbID     = "2b3c4d5e6f708192"
	payID    = "3c4d5e6f70819203"
	ms       = uint64(1_000_000)
	statusOK = 1
	statusER = 2
)

func main() {
	endpoint := flag.String("endpoint", "http://127.0.0.1:4318", "otelcat base URL")
	encoding := flag.String("encoding", "json", "json | protobuf")
	flag.Parse()

	var send func(path string) error
	switch *encoding {
	case "json":
		send = func(path string) error {
			return post(*endpoint+path, "application/json", jsonBody(path))
		}
	case "protobuf":
		send = func(path string) error {
			return post(*endpoint+path, "application/x-protobuf", protoBody(path))
		}
	default:
		fmt.Fprintf(os.Stderr, "sendspan: invalid --encoding %q\n", *encoding)
		os.Exit(2)
	}

	for _, path := range []string{"/v1/traces", "/v1/logs", "/v1/metrics"} {
		if err := send(path); err != nil {
			fmt.Fprintf(os.Stderr, "sendspan: %s: %v\n", path, err)
			os.Exit(1)
		}
	}
	fmt.Printf("sendspan: demo trace, logs and metrics delivered (%s encoding)\n", *encoding)
}

func post(url, contentType string, body []byte) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, contentType, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// --- JSON encoding -------------------------------------------------------

func jsonBody(path string) []byte {
	switch path {
	case "/v1/traces":
		return []byte(fmt.Sprintf(tracesJSON,
			baseNano, baseNano+128400000, // root
			baseNano+1200000, baseNano+13300000, // validate-cart
			baseNano+14000000, baseNano+22700000, // SELECT carts
			baseNano+30000000, baseNano+118900000, // POST /payments
			baseNano+70200000)) // exception event
	case "/v1/logs":
		return []byte(fmt.Sprintf(logsJSON, baseNano+5000000, baseNano+119000000))
	default:
		return []byte(metricsJSON)
	}
}

const tracesJSON = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}},{"key":"deployment.environment","value":{"stringValue":"dev"}}]},"scopeSpans":[{"scope":{"name":"demo-instrumentation","version":"1.0.0"},"spans":[
{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7","name":"GET /api/checkout","kind":2,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"http.route","value":{"stringValue":"/api/checkout"}},{"key":"http.response.status_code","value":{"intValue":"200"}}],"status":{"code":1}},
{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"1a2b3c4d5e6f7081","parentSpanId":"00f067aa0ba902b7","name":"validate-cart","kind":1,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"cart.items","value":{"intValue":"3"}}],"status":{"code":1}},
{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"2b3c4d5e6f708192","parentSpanId":"00f067aa0ba902b7","name":"SELECT carts","kind":3,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"db.system.name","value":{"stringValue":"postgresql"}}],"status":{"code":0}},
{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"3c4d5e6f70819203","parentSpanId":"00f067aa0ba902b7","name":"POST /payments","kind":3,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"http.request.method","value":{"stringValue":"POST"}},{"key":"peer.service","value":{"stringValue":"payments"}}],"events":[{"timeUnixNano":"%d","name":"exception","attributes":[{"key":"exception.type","value":{"stringValue":"PaymentDeclined"}}]}],"status":{"code":2,"message":"card declined"}}
]}]}]}`

const logsJSON = `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeLogs":[{"scope":{"name":"demo-instrumentation"},"logRecords":[
{"timeUnixNano":"%d","severityNumber":9,"severityText":"INFO","body":{"stringValue":"order received"},"attributes":[{"key":"order.id","value":{"intValue":"8123"}}],"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7"},
{"timeUnixNano":"%d","severityNumber":17,"severityText":"ERROR","body":{"stringValue":"payment failed"},"attributes":[{"key":"order.id","value":{"intValue":"8123"}}],"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"3c4d5e6f70819203"}
]}]}]}`

const metricsJSON = `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeMetrics":[{"metrics":[{"name":"http.server.request.duration"},{"name":"cart.items.count"}]}]}]}`

// --- protobuf encoding ---------------------------------------------------
//
// Minimal hand-rolled protobuf writer: enough of the wire format
// (varints, tags, length-delimited fields) to serialize the OTLP
// export requests above. Field numbers are from opentelemetry-proto.

type enc struct{ b []byte }

func (e *enc) varint(v uint64) {
	for v >= 0x80 {
		e.b = append(e.b, byte(v)|0x80)
		v >>= 7
	}
	e.b = append(e.b, byte(v))
}

func (e *enc) tag(field int, wt int)   { e.varint(uint64(field)<<3 | uint64(wt)) }
func (e *enc) str(field int, s string) { e.blob(field, []byte(s)) }

func (e *enc) blob(field int, b []byte) {
	e.tag(field, 2)
	e.varint(uint64(len(b)))
	e.b = append(e.b, b...)
}

func (e *enc) uvarint(field int, v uint64) {
	if v == 0 {
		return
	}
	e.tag(field, 0)
	e.varint(v)
}

func (e *enc) fixed64(field int, v uint64) {
	e.tag(field, 1)
	for i := 0; i < 8; i++ {
		e.b = append(e.b, byte(v>>(8*i)))
	}
}

func (e *enc) msg(field int, fill func(*enc)) {
	var inner enc
	fill(&inner)
	e.blob(field, inner.b)
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// strAttr encodes a common.v1.KeyValue with a stringValue.
func strAttr(e *enc, field int, key, val string) {
	e.msg(field, func(kv *enc) {
		kv.str(1, key)
		kv.msg(2, func(v *enc) { v.str(1, val) })
	})
}

// intAttr encodes a common.v1.KeyValue with an intValue.
func intAttr(e *enc, field int, key string, val uint64) {
	e.msg(field, func(kv *enc) {
		kv.str(1, key)
		kv.msg(2, func(v *enc) { v.uvarint(3, val) })
	})
}

func checkoutResource(e *enc) {
	e.msg(1, func(r *enc) { // resource
		strAttr(r, 1, "service.name", "checkout")
		strAttr(r, 1, "deployment.environment", "dev")
	})
}

type spanSpec struct {
	id, parent string
	name       string
	kind       uint64
	start, end uint64
	status     uint64
	statusMsg  string
	fill       func(*enc)
}

func writeSpan(scope *enc, s spanSpec) {
	scope.msg(2, func(sp *enc) { // spans
		sp.blob(1, mustHex(traceID))
		sp.blob(2, mustHex(s.id))
		if s.parent != "" {
			sp.blob(4, mustHex(s.parent))
		}
		sp.str(5, s.name)
		sp.uvarint(6, s.kind)
		sp.fixed64(7, s.start)
		sp.fixed64(8, s.end)
		if s.fill != nil {
			s.fill(sp)
		}
		sp.msg(15, func(st *enc) { // status
			if s.statusMsg != "" {
				st.str(2, s.statusMsg)
			}
			st.uvarint(3, s.status)
		})
	})
}

func protoTraces() []byte {
	var e enc
	e.msg(1, func(rs *enc) { // resource_spans
		checkoutResource(rs)
		rs.msg(2, func(ss *enc) { // scope_spans
			ss.msg(1, func(sc *enc) { // scope
				sc.str(1, "demo-instrumentation")
				sc.str(2, "1.0.0")
			})
			writeSpan(ss, spanSpec{
				id: rootID, name: "GET /api/checkout", kind: 2,
				start: baseNano, end: baseNano + 128400000, status: statusOK,
				fill: func(sp *enc) {
					strAttr(sp, 9, "http.request.method", "GET")
					strAttr(sp, 9, "http.route", "/api/checkout")
					intAttr(sp, 9, "http.response.status_code", 200)
				},
			})
			writeSpan(ss, spanSpec{
				id: cartID, parent: rootID, name: "validate-cart", kind: 1,
				start: baseNano + 1200000, end: baseNano + 13300000, status: statusOK,
				fill: func(sp *enc) { intAttr(sp, 9, "cart.items", 3) },
			})
			writeSpan(ss, spanSpec{
				id: dbID, parent: rootID, name: "SELECT carts", kind: 3,
				start: baseNano + 14000000, end: baseNano + 22700000,
				fill: func(sp *enc) { strAttr(sp, 9, "db.system.name", "postgresql") },
			})
			writeSpan(ss, spanSpec{
				id: payID, parent: rootID, name: "POST /payments", kind: 3,
				start: baseNano + 30000000, end: baseNano + 118900000,
				status: statusER, statusMsg: "card declined",
				fill: func(sp *enc) {
					strAttr(sp, 9, "http.request.method", "POST")
					strAttr(sp, 9, "peer.service", "payments")
					sp.msg(11, func(ev *enc) { // events
						ev.fixed64(1, baseNano+70200000)
						ev.str(2, "exception")
						strAttr(ev, 3, "exception.type", "PaymentDeclined")
					})
				},
			})
		})
	})
	return e.b
}

func protoLogs() []byte {
	rec := func(sl *enc, t uint64, sev uint64, sevText, body string, spanID string) {
		sl.msg(2, func(lr *enc) { // log_records
			lr.fixed64(1, t)
			lr.uvarint(2, sev)
			lr.str(3, sevText)
			lr.msg(5, func(b *enc) { b.str(1, body) })
			intAttr(lr, 6, "order.id", 8123)
			lr.blob(9, mustHex(traceID))
			lr.blob(10, mustHex(spanID))
		})
	}
	var e enc
	e.msg(1, func(rl *enc) { // resource_logs
		rl.msg(1, func(r *enc) { strAttr(r, 1, "service.name", "checkout") })
		rl.msg(2, func(sl *enc) { // scope_logs
			sl.msg(1, func(sc *enc) { sc.str(1, "demo-instrumentation") })
			rec(sl, baseNano+5000000, 9, "INFO", "order received", rootID)
			rec(sl, baseNano+119000000, 17, "ERROR", "payment failed", payID)
		})
	})
	return e.b
}

func protoMetrics() []byte {
	var e enc
	e.msg(1, func(rm *enc) { // resource_metrics
		rm.msg(1, func(r *enc) { strAttr(r, 1, "service.name", "checkout") })
		rm.msg(2, func(sm *enc) { // scope_metrics
			sm.msg(2, func(m *enc) { m.str(1, "http.server.request.duration") })
			sm.msg(2, func(m *enc) { m.str(1, "cart.items.count") })
		})
	})
	return e.b
}

func protoBody(path string) []byte {
	switch path {
	case "/v1/traces":
		return protoTraces()
	case "/v1/logs":
		return protoLogs()
	default:
		return protoMetrics()
	}
}
