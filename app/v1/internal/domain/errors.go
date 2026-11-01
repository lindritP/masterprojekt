// Package domain holds the CCSRA entities, the case state machine and its
// invariants (SRS §3). It imports no adapter package and no provider SDK
// (AR-004); scripts/check-boundaries.sh enforces this in CI.
//
// Every mutation is a method on the Case aggregate. It checks authorization
// and preconditions before changing anything, increments the version once
// (INV-6) and returns the audit entries the repository must write in the same
// transaction (INV-3).
package domain

import (
	"errors"
	"fmt"
)

// Error codes from the SRS §5.2 error catalogue that the domain raises.
// Mapping a code to an HTTP status is the services' concern.
const (
	CodeValidationFailed         = "VALIDATION_FAILED"
	CodeInvalidCaseType          = "INVALID_CASE_TYPE"
	CodeForbiddenRole            = "FORBIDDEN_ROLE"
	CodeForbiddenNotOwner        = "FORBIDDEN_NOT_OWNER"
	CodeCaseNotFound             = "CASE_NOT_FOUND"
	CodeDocumentNotFound         = "DOCUMENT_NOT_FOUND"
	CodeInvalidStateTransition   = "INVALID_STATE_TRANSITION"
	CodeConcurrentModification   = "CONCURRENT_MODIFICATION"
	CodeDocumentAlreadyValidated = "DOCUMENT_ALREADY_VALIDATED"
	CodeNoDocumentsAttached      = "NO_DOCUMENTS_ATTACHED"
	CodeDocumentsImmutable       = "DOCUMENTS_IMMUTABLE"
	CodeUnsupportedMediaType     = "UNSUPPORTED_MEDIA_TYPE"
	CodePayloadTooLarge          = "PAYLOAD_TOO_LARGE"
	CodeDocumentLimitExceeded    = "DOCUMENT_LIMIT_EXCEEDED"
	CodeDecisionReasonRequired   = "DECISION_REASON_REQUIRED"
)

// Error is a domain rule violation carrying a §5.2 code.
type Error struct {
	Code   string
	Field  string // the offending input field, if any
	Detail string
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s (%s): %s", e.Code, e.Field, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// CodeOf returns the §5.2 code of err, or "" if err is not a domain error.
func CodeOf(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return ""
}

func fail(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

func invalidField(field, format string, args ...any) error {
	return &Error{Code: CodeValidationFailed, Field: field, Detail: fmt.Sprintf(format, args...)}
}
