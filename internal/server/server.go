// Package server implements the OTLP/HTTP receiver: three POST routes
// (/v1/traces, /v1/logs, /v1/metrics), both content types (protobuf and
// JSON), optional gzip request bodies, and spec-shaped success/error
// responses so real SDK exporters treat otelcat as a compliant endpoint.
package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/JaydenCJ/otelcat/internal/otlp"
	"github.com/JaydenCJ/otelcat/internal/render"
)

// DefaultMaxBody caps request bodies at 16 MiB after decompression —
// far above any sane SDK batch, low enough to shrug off a gzip bomb.
const DefaultMaxBody = 16 << 20

// Stats counts what the sink has seen; read for the shutdown summary.
type Stats struct {
	Requests   int
	Spans      int
	LogRecords int
	Metrics    int
}

// Handler is the OTLP/HTTP endpoint. It serializes rendering through a
// mutex so concurrent exporters cannot interleave their trace trees.
type Handler struct {
	mu       sync.Mutex
	renderer *render.Renderer
	errw     io.Writer // where decode warnings go (stderr in the CLI)
	maxBody  int64
	stats    Stats
}

// New returns a Handler that renders through r and reports request-level
// problems to errw.
func New(r *render.Renderer, errw io.Writer, maxBody int64) *Handler {
	if maxBody <= 0 {
		maxBody = DefaultMaxBody
	}
	return &Handler{renderer: r, errw: errw, maxBody: maxBody}
}

// Stats returns a copy of the counters.
func (h *Handler) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}

// signal is what a route does with a decoded body.
type signal int

const (
	sigTraces signal = iota
	sigLogs
	sigMetrics
)

// ServeHTTP routes OTLP export requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var sig signal
	switch req.URL.Path {
	case "/v1/traces":
		sig = sigTraces
	case "/v1/logs":
		sig = sigLogs
	case "/v1/metrics":
		sig = sigMetrics
	case "/":
		// A friendly landing line for people who open the port in a
		// browser to check the thing is alive.
		fmt.Fprintln(w, "otelcat: OTLP/HTTP sink. POST to /v1/traces, /v1/logs or /v1/metrics.")
		return
	default:
		http.NotFound(w, req)
		return
	}
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.fail(w, req, http.StatusMethodNotAllowed, "OTLP export must be a POST")
		return
	}

	ct := mediaType(req.Header.Get("Content-Type"))
	isProto := ct == "application/x-protobuf"
	isJSON := ct == "application/json"
	if !isProto && !isJSON {
		h.fail(w, req, http.StatusUnsupportedMediaType,
			fmt.Sprintf("unsupported content type %q (use application/x-protobuf or application/json)", ct))
		return
	}

	body, err := h.readBody(req)
	if err != nil {
		h.fail(w, req, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.dispatch(sig, isProto, body); err != nil {
		h.fail(w, req, http.StatusBadRequest, err.Error())
		return
	}

	// OTLP success: full response message. For JSON that is an empty
	// object; for protobuf an empty message serializes to zero bytes.
	if isProto {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "{}")
}

// dispatch decodes and renders one payload under the render lock.
func (h *Handler) dispatch(sig signal, isProto bool, body []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch sig {
	case sigTraces:
		var td *otlp.TraceData
		var err error
		if isProto {
			td, err = otlp.DecodeTracesProto(body)
		} else {
			td, err = otlp.DecodeTracesJSON(body)
		}
		if err != nil {
			return err
		}
		h.stats.Spans += len(td.Spans)
		h.renderer.Traces(td)
	case sigLogs:
		var ld *otlp.LogData
		var err error
		if isProto {
			ld, err = otlp.DecodeLogsProto(body)
		} else {
			ld, err = otlp.DecodeLogsJSON(body)
		}
		if err != nil {
			return err
		}
		h.stats.LogRecords += len(ld.Records)
		h.renderer.Logs(ld)
	case sigMetrics:
		var sum *otlp.MetricsSummary
		var err error
		if isProto {
			sum, err = otlp.SummarizeMetricsProto(body)
		} else {
			sum, err = otlp.SummarizeMetricsJSON(body)
		}
		if err != nil {
			return err
		}
		h.stats.Metrics += sum.MetricCount
		h.renderer.Metrics(sum)
	}
	// Only successfully decoded exports count; rejections are logged
	// separately and must not inflate the shutdown summary.
	h.stats.Requests++
	return nil
}

// readBody reads the (possibly gzipped) request body under the size cap.
func (h *Handler) readBody(req *http.Request) ([]byte, error) {
	var rd io.Reader = req.Body
	switch enc := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(req.Body)
		if err != nil {
			return nil, fmt.Errorf("invalid gzip body: %v", err)
		}
		defer gz.Close()
		rd = gz
	default:
		return nil, fmt.Errorf("unsupported content encoding %q", enc)
	}
	body, err := io.ReadAll(io.LimitReader(rd, h.maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading body: %v", err)
	}
	if int64(len(body)) > h.maxBody {
		return nil, fmt.Errorf("body exceeds %d byte limit", h.maxBody)
	}
	return body, nil
}

// fail writes an OTLP-shaped error (google.rpc.Status as JSON) and logs
// one line to errw so a misconfigured SDK is diagnosable from the sink.
func (h *Handler) fail(w http.ResponseWriter, req *http.Request, code int, msg string) {
	if h.errw != nil {
		fmt.Fprintf(h.errw, "otelcat: rejected %s %s: %s\n", req.Method, req.URL.Path, msg)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    rpcCode(code),
		"message": msg,
	})
}

// rpcCode maps the HTTP status onto the google.rpc.Code the OTLP spec
// pairs with it (3 INVALID_ARGUMENT, 12 UNIMPLEMENTED).
func rpcCode(httpStatus int) int {
	switch httpStatus {
	case http.StatusBadRequest:
		return 3
	case http.StatusMethodNotAllowed, http.StatusUnsupportedMediaType:
		return 12
	default:
		return 2 // UNKNOWN
	}
}

// mediaType strips parameters like "; charset=utf-8" from a Content-Type.
func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
