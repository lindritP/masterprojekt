package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
)

// HandlerFunc is an HTTP handler that returns its error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle adapts h, writing a returned error as a problem.
func Handle(log *slog.Logger, h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	})
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// DecodeJSON reads a JSON body of at most maxBytes into v, rejecting unknown
// fields. Failures are VALIDATION_FAILED or PAYLOAD_TOO_LARGE problems.
func DecodeJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return NewProblem(domain.CodePayloadTooLarge, "request body exceeds %d bytes", maxBytes)
		}
		return NewProblem(domain.CodeValidationFailed, "invalid JSON body: %v", err)
	}
	if dec.More() {
		return NewProblem(domain.CodeValidationFailed, "request body holds more than one JSON value")
	}
	return nil
}

// ETag renders a case version as an entity tag.
func ETag(version int) string { return strconv.Quote(strconv.Itoa(version)) }

// IfMatch parses a required If-Match header into a case version.
func IfMatch(r *http.Request) (int, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("If-Match")), "W/")
	if raw == "" {
		return 0, FieldProblem("If-Match", "If-Match is required on state-changing requests")
	}
	v, err := strconv.Atoi(strings.Trim(raw, `"`))
	if err != nil || v < 1 {
		return 0, FieldProblem("If-Match", "If-Match must carry a case version, got %q", raw)
	}
	return v, nil
}

// IdempotencyKey returns the required Idempotency-Key header.
func IdempotencyKey(r *http.Request) (string, error) {
	k := strings.TrimSpace(r.Header.Get(HeaderIdempotencyKey))
	if k == "" || len(k) > 200 {
		return "", FieldProblem(HeaderIdempotencyKey, "Idempotency-Key is required and at most 200 characters")
	}
	return k, nil
}

// Millis formats a timestamp as RFC 3339 UTC with millisecond precision (§5.1).
func Millis(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// MillisPtr formats an optional timestamp, or returns nil.
func MillisPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := Millis(*t)
	return &s
}

// Now is the time domain operations are stamped with: UTC, millisecond precision.
func Now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// statusRecorder captures the status code for logs and metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Observe logs every request at debug level and records RED metrics labelled
// by route pattern. It also turns panics into INTERNAL_ERROR problems.
func Observe(log *slog.Logger, reg *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				WriteError(rec, r, log, fmt.Errorf("panic: %v", v))
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			reg.requests.With(prometheus.Labels{"route": route, "code": strconv.Itoa(rec.status)}).Inc()
			reg.duration.With(prometheus.Labels{"route": route}).Observe(time.Since(start).Seconds())
			log.DebugContext(r.Context(), "request", "method", r.Method, "route", route, "status", rec.status, "durationMs", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(rec, r)
	})
}

// drain discards and closes a response body so the connection can be reused.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	body.Close()
}
