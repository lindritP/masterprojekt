// Package platform holds what all four V1 services share and what carries no
// provider coupling: configuration from the environment (FR-015, DR-007),
// structured logs (FR-012), correlation ids (FR-014), RFC 9457 problems
// (FR-017), health and metrics endpoints (FR-010, FR-011), graceful shutdown
// (FR-016) and the internal HTTP client.
package platform

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Version is the build SHA, set at link time (DR-003).
var Version = "dev"

// Header names of the internal API contract (SRS §5.1, FR-103).
const (
	HeaderCorrelationID  = "X-Correlation-Id"
	HeaderSubjectID      = "X-Subject-Id"
	HeaderSubjectRoles   = "X-Subject-Roles"
	HeaderAuthLevel      = "X-Auth-Level"
	HeaderService        = "X-Service-Name"
	HeaderIdempotencyKey = "Idempotency-Key"
)

// Subject is the authenticated caller as propagated by identity-gateway.
type Subject struct {
	ID        string
	Roles     []string
	AuthLevel string
}

type ctxKey int

const (
	keyCorrelation ctxKey = iota
	keySubject
	keyService
)

// WithCorrelationID returns ctx carrying id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyCorrelation, id)
}

// CorrelationID returns the correlation id carried by ctx, or "".
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(keyCorrelation).(string)
	return id
}

// WithSubject returns ctx carrying s.
func WithSubject(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, keySubject, s)
}

// SubjectFrom returns the subject carried by ctx.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	s, ok := ctx.Value(keySubject).(Subject)
	return s, ok && s.ID != ""
}

// CallerService returns the internal service named in X-Service-Name, if any.
func CallerService(ctx context.Context) string {
	s, _ := ctx.Value(keyService).(string)
	return s
}

// InternalContext is the middleware of internal services: it reads the
// correlation id (assigning one if missing), the propagated subject and the
// calling service into the request context.
func InternalContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if id := r.Header.Get(HeaderSubjectID); id != "" {
			var roles []string
			for role := range strings.SplitSeq(r.Header.Get(HeaderSubjectRoles), ",") {
				if role = strings.TrimSpace(role); role != "" {
					roles = append(roles, role)
				}
			}
			ctx = WithSubject(ctx, Subject{ID: id, Roles: roles, AuthLevel: r.Header.Get(HeaderAuthLevel)})
		}
		if svc := r.Header.Get(HeaderService); svc != "" {
			ctx = context.WithValue(ctx, keyService, svc)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Correlation assigns or adopts X-Correlation-Id and echoes it (FR-014).
func Correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderCorrelationID)
		if id == "" || len(id) > 128 {
			id = uuid.Must(uuid.NewV7()).String()
		}
		w.Header().Set(HeaderCorrelationID, id)
		next.ServeHTTP(w, r.WithContext(WithCorrelationID(r.Context(), id)))
	})
}
