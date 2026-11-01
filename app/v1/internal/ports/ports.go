// Package ports declares the provider-agnostic interfaces through which V1
// reaches every primary coupling point (SRS §7.2). Like the domain, it imports
// no adapter package and no provider SDK (AR-002, AR-004).
package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
)

// ErrUnsupported is returned when the underlying provider cannot offer the
// requested capability at all. Adapters MUST return it rather than silently
// succeeding. It is the input signal for the Capability Matrix. (AR-005)
var ErrUnsupported = errors.New("capability not supported by this provider")

// ErrNotFound is returned when the addressed object, case or record does not exist.
var ErrNotFound = errors.New("not found")

// ErrBadCursor is returned for a pagination cursor that cannot be decoded.
var ErrBadCursor = errors.New("invalid cursor")

// ErrUnauthenticated is returned by IdentityVerifier for any token it does not accept.
var ErrUnauthenticated = errors.New("unauthenticated")

// ---------- CP-2: object storage ----------

type PutConstraints struct {
	ContentType  string
	MaxSizeBytes int64
	TTL          time.Duration
}

type PresignedURL struct {
	URL             string
	Method          string            // "GET", "PUT" or "POST" (form upload)
	RequiredHeaders map[string]string // headers the client MUST send
	FormFields      map[string]string // POST only: fields to send, unchanged, before the file
	ExpiresAt       time.Time
}

type ObjectInfo struct {
	Key       string
	SizeBytes int64
	ETag      string
	UpdatedAt time.Time
}

type DocumentStore interface {
	// PresignUpload returns a PUT or POST upload, whichever lets the storage
	// service itself enforce c. (FR-301, FR-302)
	PresignUpload(ctx context.Context, key string, c PutConstraints) (PresignedURL, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error)
	Head(ctx context.Context, key string) (ObjectInfo, error)              // ErrNotFound if absent
	Delete(ctx context.Context, key string) error                          // nil if already absent
	ApplyRetention(ctx context.Context, key string, until time.Time) error // may return ErrUnsupported
}

// ---------- CP-3: relational state ----------

// Cursor is an opaque pagination token. Empty means "from the start" in a
// request and "no further page" in a response.
type Cursor string

type ListFilter struct {
	SubjectID string          // empty: all subjects (caseworkers only)
	State     domain.State    // empty: any state
	CaseType  domain.CaseType // empty: any type
	Limit     int
	After     Cursor
}

type CaseRepository interface {
	// AllocateReference returns the next human-readable reference for year.
	AllocateReference(ctx context.Context, year int) (string, error)

	// Create stores a new case with its creation audit entries.
	Create(ctx context.Context, c *domain.Case, audit []domain.AuditEntry) error

	// Get loads a case with its documents and their validation results.
	// Returns ErrNotFound if the case does not exist.
	Get(ctx context.Context, id uuid.UUID) (*domain.Case, error)

	List(ctx context.Context, f ListFilter) ([]domain.CaseSummary, Cursor, error)

	// Transition loads the case under a row lock, applies fn, and writes the
	// case, its documents and the returned audit entries in ONE transaction.
	// If fn fails, nothing is written. A non-zero expectedVersion that is not
	// current fails with a CONCURRENT_MODIFICATION domain error. (INV-3, INV-6)
	Transition(ctx context.Context, id uuid.UUID, expectedVersion int,
		fn func(*domain.Case) ([]domain.AuditEntry, error)) error

	// TransitionOnce is Transition guarded by an idempotency key within a
	// scope (§5.1). If the key was already used, nothing is applied and
	// replayed=true; the version check is skipped for a replay.
	TransitionOnce(ctx context.Context, scope, key string, id uuid.UUID, expectedVersion int,
		fn func(*domain.Case) ([]domain.AuditEntry, error)) (replayed bool, err error)

	// CaseIDOfDocument returns the case a document belongs to, or ErrNotFound.
	CaseIDOfDocument(ctx context.Context, documentID uuid.UUID) (uuid.UUID, error)

	// RecordValidationResult applies the worker's result to its case through
	// domain.Case.RecordValidation, in one transaction, idempotently on
	// idempotencyKey. replayed=true means the key was already seen or the
	// result was an identical repeat, and nothing changed. (FR-206, INV-8)
	RecordValidationResult(ctx context.Context, idempotencyKey string,
		r domain.ValidationResult, op domain.Op) (replayed bool, err error)

	// ListAudit returns a case's audit trail in write order (FR-212).
	ListAudit(ctx context.Context, caseID uuid.UUID, limit int, after Cursor) ([]domain.AuditEntry, Cursor, error)
}

// ---------- CP-4: messaging ----------

type Event struct {
	Type          string
	Source        string
	ID            string
	Time          time.Time
	Subject       string
	CorrelationID string
	Data          json.RawMessage
}

type EventBus interface {
	Publish(ctx context.Context, e Event, partitionKey string) error
}

type Outcome int

const (
	OutcomeAck        Outcome = iota // processed successfully
	OutcomeRetry                     // transient failure; redeliver
	OutcomeDeadLetter                // permanent failure; do not redeliver
)

type Delivery struct {
	MessageID     string
	DeliveryCount int // 1 on first delivery
	PartitionKey  string
	Event         Event
}

type EventConsumer interface {
	// Subscribe blocks until ctx is cancelled. Handler outcomes drive
	// acknowledgement, retry and dead-lettering.
	Subscribe(ctx context.Context, handler func(context.Context, Delivery) Outcome) error
}

// ---------- CP-5: identity ----------

type Subject struct {
	ID        string
	Roles     []string
	AuthLevel string
}

type IdentityVerifier interface {
	// Verify returns ErrUnauthenticated (wrapped) for any token it rejects.
	Verify(ctx context.Context, rawToken string) (Subject, error)
}

// ---------- CP-6: secrets ----------

type SecretProvider interface {
	Get(ctx context.Context, name string) (string, error) // ErrNotFound if unset
}
