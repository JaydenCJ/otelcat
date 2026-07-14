// HTTP handler tests, run in-process against httptest recorders — no
// sockets, no network. They pin the OTLP/HTTP contract: routes, content
// types, gzip, response bodies, error codes and the stats counters.
package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JaydenCJ/otelcat/internal/render"
)

const tracesJSON = `{"resourceSpans":[{
	"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
	"scopeSpans":[{"spans":[
		{"traceId":"4bf92f3577b34da6a3ce929d0e0e4736","spanId":"00f067aa0ba902b7",
		 "name":"GET /demo","kind":2,"startTimeUnixNano":"1000","endTimeUnixNano":"129000000"}
	]}]}]}`

const logsJSON = `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
	{"timeUnixNano":"5000","severityNumber":9,"body":{"stringValue":"hello"}}
]}]}]}`

const metricsJSON = `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"m1"}]}]}]}`

// newHandler wires a handler to in-memory buffers for stdout/stderr.
func newHandler() (*Handler, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	r := render.New(&out, render.Options{})
	return New(r, &errw, 0), &out, &errw
}

func do(h *Handler, method, path, contentType string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestTracesJSONRenderedAndAcknowledged(t *testing.T) {
	h, out, _ := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "application/json", []byte(tracesJSON), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	// Success body is a full-response JSON object per the OTLP spec.
	if strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("success body wrong: %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("response content type wrong: %q", ct)
	}
	if !strings.Contains(out.String(), "GET /demo") || !strings.Contains(out.String(), "checkout") {
		t.Fatalf("span not rendered:\n%s", out.String())
	}
	// A charset parameter on the Content-Type must not break dispatch.
	h2, out2, _ := newHandler()
	w2 := do(h2, http.MethodPost, "/v1/traces", "application/json; charset=utf-8", []byte(tracesJSON), nil)
	if w2.Code != http.StatusOK || !strings.Contains(out2.String(), "GET /demo") {
		t.Fatalf("charset parameter broke dispatch: %d", w2.Code)
	}
}

func TestTracesGzipBody(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(tracesJSON)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	h, out, _ := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "application/json", gz.Bytes(),
		map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusOK {
		t.Fatalf("gzip request rejected: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(out.String(), "GET /demo") {
		t.Fatalf("gzip body not decoded:\n%s", out.String())
	}
}

func TestBadContentEncodingRejected(t *testing.T) {
	// Corrupt gzip and an unsupported codec both get a clean 400,
	// never a panic or a silent empty render.
	h, _, _ := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "application/json", []byte("not gzip"),
		map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for corrupt gzip, got %d", w.Code)
	}
	w = do(h, http.MethodPost, "/v1/traces", "application/json", []byte(tracesJSON),
		map[string]string{"Content-Encoding": "zstd"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for zstd, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "zstd") {
		t.Fatalf("error should name the encoding: %s", w.Body.String())
	}
}

func TestUnsupportedContentTypeGets415(t *testing.T) {
	h, _, _ := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "text/plain", []byte("hi"), nil)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", w.Code)
	}
}

func TestGetMethodGets405WithAllowHeader(t *testing.T) {
	h, _, _ := newHandler()
	w := do(h, http.MethodGet, "/v1/traces", "", nil, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", w.Code)
	}
	if w.Header().Get("Allow") != "POST" {
		t.Fatalf("Allow header missing: %q", w.Header().Get("Allow"))
	}
}

func TestUnknownPathGets404(t *testing.T) {
	h, _, _ := newHandler()
	w := do(h, http.MethodPost, "/v2/traces", "application/json", []byte("{}"), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestRootPathIsFriendly(t *testing.T) {
	h, _, _ := newHandler()
	w := do(h, http.MethodGet, "/", "", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/v1/traces") {
		t.Fatalf("landing line wrong: %d %s", w.Code, w.Body.String())
	}
}

func TestMalformedJSONGetsRPCStatusError(t *testing.T) {
	h, _, errw := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "application/json", []byte("{nope"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	// Error body is a google.rpc.Status-shaped JSON object.
	var status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("error body not JSON: %v", err)
	}
	if status.Code != 3 || status.Message == "" {
		t.Fatalf("rpc status wrong: %+v", status)
	}
	// And the rejection is logged for the operator.
	if !strings.Contains(errw.String(), "rejected POST /v1/traces") {
		t.Fatalf("rejection not logged: %s", errw.String())
	}
}

func TestProtobufRequestAcknowledgedWithProtobuf(t *testing.T) {
	// An empty ExportTraceServiceRequest is zero bytes — still a valid
	// protobuf message and the smallest possible exporter payload.
	h, _, _ := newHandler()
	w := do(h, http.MethodPost, "/v1/traces", "application/x-protobuf", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("empty proto request rejected: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-protobuf" {
		t.Fatalf("proto response content type wrong: %q", ct)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("empty proto response expected, got %d bytes", w.Body.Len())
	}
}

func TestBodyLimitEnforcedPostDecompression(t *testing.T) {
	// A tiny gzip bomb: 1 KiB limit, 1 MiB of zeros. The cap must apply
	// to the inflated size, or compressed bombs sail through.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	var out, errw bytes.Buffer
	h := New(render.New(&out, render.Options{}), &errw, 1024)
	w := do(h, http.MethodPost, "/v1/logs", "application/json", gz.Bytes(),
		map[string]string{"Content-Encoding": "gzip"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for oversized body, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "limit") {
		t.Fatalf("error should mention the limit: %s", w.Body.String())
	}
}

func TestLogsAndMetricsRoutes(t *testing.T) {
	h, out, _ := newHandler()
	if w := do(h, http.MethodPost, "/v1/logs", "application/json", []byte(logsJSON), nil); w.Code != http.StatusOK {
		t.Fatalf("logs route failed: %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/v1/metrics", "application/json", []byte(metricsJSON), nil); w.Code != http.StatusOK {
		t.Fatalf("metrics route failed: %d", w.Code)
	}
	if !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), "m1") {
		t.Fatalf("logs/metrics not rendered:\n%s", out.String())
	}
}

func TestStatsCountersAccumulate(t *testing.T) {
	h, _, _ := newHandler()
	do(h, http.MethodPost, "/v1/traces", "application/json", []byte(tracesJSON), nil)
	do(h, http.MethodPost, "/v1/traces", "application/json", []byte(tracesJSON), nil)
	do(h, http.MethodPost, "/v1/logs", "application/json", []byte(logsJSON), nil)
	do(h, http.MethodPost, "/v1/metrics", "application/json", []byte(metricsJSON), nil)
	st := h.Stats()
	if st.Requests != 4 || st.Spans != 2 || st.LogRecords != 1 || st.Metrics != 1 {
		t.Fatalf("stats wrong: %+v", st)
	}
}

func TestRejectedRequestsDoNotCountSpans(t *testing.T) {
	h, _, _ := newHandler()
	do(h, http.MethodPost, "/v1/traces", "application/json", []byte("{bad"), nil)
	if st := h.Stats(); st.Spans != 0 || st.Requests != 0 {
		t.Fatalf("rejected payload counted toward stats: %+v", st)
	}
}
