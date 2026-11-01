package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Codes of the SRS §5.2 catalogue that are not raised by the domain.
const (
	CodeUnauthenticated     = "UNAUTHENTICATED"
	CodeUploadNotCompleted  = "UPLOAD_NOT_COMPLETED"
	CodeRateLimited         = "RATE_LIMITED"
	CodeUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"
	CodeInternalError       = "INTERNAL_ERROR"
)

// statusOf maps every §5.2 code to its HTTP status.
var statusOf = map[string]int{
	domain.CodeValidationFailed:         http.StatusBadRequest,
	domain.CodeInvalidCaseType:          http.StatusBadRequest,
	CodeUnauthenticated:                 http.StatusUnauthorized,
	domain.CodeForbiddenRole:            http.StatusForbidden,
	domain.CodeForbiddenNotOwner:        http.StatusForbidden,
	domain.CodeCaseNotFound:             http.StatusNotFound,
	domain.CodeDocumentNotFound:         http.StatusNotFound,
	domain.CodeInvalidStateTransition:   http.StatusConflict,
	domain.CodeConcurrentModification:   http.StatusConflict,
	CodeUploadNotCompleted:              http.StatusConflict,
	domain.CodeDocumentAlreadyValidated: http.StatusConflict,
	domain.CodeNoDocumentsAttached:      http.StatusConflict,
	domain.CodeDocumentsImmutable:       http.StatusConflict,
	domain.CodeUnsupportedMediaType:     http.StatusUnsupportedMediaType,
	domain.CodePayloadTooLarge:          http.StatusRequestEntityTooLarge,
	domain.CodeDocumentLimitExceeded:    http.StatusUnprocessableEntity,
	domain.CodeDecisionReasonRequired:   http.StatusUnprocessableEntity,
	CodeRateLimited:                     http.StatusTooManyRequests,
	CodeUpstreamUnavailable:             http.StatusServiceUnavailable,
	CodeInternalError:                   http.StatusInternalServerError,
}

// Problem is an error that is reported as an RFC 9457 problem (FR-017).
type Problem struct {
	Status int
	Code   string
	Detail string
	Field  string
}

func (p *Problem) Error() string { return fmt.Sprintf("%d %s: %s", p.Status, p.Code, p.Detail) }

// NewProblem builds a problem whose status follows from its code.
func NewProblem(code, format string, args ...any) *Problem {
	status, ok := statusOf[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	return &Problem{Status: status, Code: code, Detail: fmt.Sprintf(format, args...)}
}

// FieldProblem is a VALIDATION_FAILED problem about one input field.
func FieldProblem(field, format string, args ...any) *Problem {
	p := NewProblem(domain.CodeValidationFailed, format, args...)
	p.Field = field
	return p
}

type problemBody struct {
	Type          string       `json:"type"`
	Title         string       `json:"title"`
	Status        int          `json:"status"`
	Detail        string       `json:"detail"`
	Code          string       `json:"code"`
	CorrelationID string       `json:"correlationId,omitempty"`
	Errors        []fieldError `json:"errors,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// AsProblem converts any error into the problem it is reported as.
// Unclassified errors become INTERNAL_ERROR with a generic detail.
func AsProblem(err error) *Problem {
	if p, ok := errors.AsType[*Problem](err); ok {
		return p
	}
	if e, ok := errors.AsType[*domain.Error](err); ok {
		p := NewProblem(e.Code, "%s", e.Detail)
		p.Field = e.Field
		return p
	}
	if errors.Is(err, ports.ErrUnauthenticated) {
		return NewProblem(CodeUnauthenticated, "missing, malformed or invalid token")
	}
	return NewProblem(CodeInternalError, "unexpected error")
}

// WriteError writes err as a problem. INTERNAL_ERROR is logged with full
// context, but its detail stays generic (SRS §5.2).
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p := AsProblem(err)
	if p.Code == CodeInternalError {
		log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	WriteProblem(w, r, p)
}

// WriteProblem writes p as application/problem+json.
func WriteProblem(w http.ResponseWriter, r *http.Request, p *Problem) {
	body := problemBody{
		Type:          "https://ccsra.example/errors/" + p.Code,
		Title:         title(p.Code),
		Status:        p.Status,
		Detail:        p.Detail,
		Code:          p.Code,
		CorrelationID: CorrelationID(r.Context()),
	}
	if p.Field != "" {
		body.Errors = []fieldError{{Field: p.Field, Message: p.Detail}}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(body)
}

// DecodeProblem reads a problem body returned by another service.
func DecodeProblem(status int, data []byte) *Problem {
	var b problemBody
	if json.Unmarshal(data, &b) != nil || b.Code == "" {
		return &Problem{Status: status, Code: CodeInternalError, Detail: fmt.Sprintf("upstream returned status %d", status)}
	}
	p := &Problem{Status: status, Code: b.Code, Detail: b.Detail}
	if len(b.Errors) > 0 {
		p.Field = b.Errors[0].Field
	}
	return p
}

// title turns INVALID_STATE_TRANSITION into "Invalid state transition".
func title(code string) string {
	s := strings.ToLower(strings.ReplaceAll(code, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
