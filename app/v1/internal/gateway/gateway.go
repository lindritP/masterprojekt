// Package gateway is identity-gateway (SRS §4.2): the only service reachable
// from outside the cluster (FR-100). It verifies the bearer token, maps its
// claims onto the internal subject headers (FR-103), enforces coarse role
// rules (FR-104) and forwards to case-service and intake-service (FR-105).
// The raw token is never forwarded.
package gateway

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Server is the public edge.
type Server struct {
	Verifier     ports.IdentityVerifier
	Cases        *Upstream
	Intake       *Upstream
	MaxBodyBytes int64      // CCSRA_MAX_BODY_BYTES (FR-108)
	RateLimit    rate.Limit // per subject, CCSRA_RATE_LIMIT_RPS (FR-107)
	CORSOrigins  []string   // CCSRA_CORS_ALLOWED_ORIGINS, for the local API explorer
	Log          *slog.Logger

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

type route struct {
	method, pattern string
	upstream        func(*Server) *Upstream
	target          func(*http.Request) string
	caseworkerOnly  bool
}

// The public API of SRS §5.3, mapped onto the internal APIs of §5.4 and §5.5.
var routes = []route{
	{"POST", "/api/v1/cases", cases, fixed("/internal/v1/cases"), false},
	{"GET", "/api/v1/cases", cases, fixed("/internal/v1/cases"), false},
	{"GET", "/api/v1/cases/{caseId}", cases, caseTarget(""), false},
	{"POST", "/api/v1/cases/{caseId}/documents", intake, caseTarget("/documents"), false},
	{"POST", "/api/v1/cases/{caseId}/documents/{documentId}/complete", intake, documentTarget("/complete"), false},
	{"GET", "/api/v1/cases/{caseId}/documents/{documentId}/download", intake, documentTarget("/download-url"), false},
	{"DELETE", "/api/v1/cases/{caseId}/documents/{documentId}", intake, documentTarget(""), false},
	{"POST", "/api/v1/cases/{caseId}/submit", cases, caseTarget("/submit"), false},
	{"GET", "/api/v1/cases/{caseId}/events", cases, caseTarget("/events"), false},
	{"GET", "/api/v1/worker/cases", cases, fixed("/internal/v1/cases"), true},
	{"GET", "/api/v1/worker/cases/{caseId}", cases, caseTarget(""), true},
	{"POST", "/api/v1/worker/cases/{caseId}/decision", cases, caseTarget("/decision"), true},
	{"GET", "/api/v1/worker/cases/{caseId}/export", cases, caseTarget("/export"), true},
}

func cases(s *Server) *Upstream  { return s.Cases }
func intake(s *Server) *Upstream { return s.Intake }

func fixed(path string) func(*http.Request) string {
	return func(*http.Request) string { return path }
}

func caseTarget(suffix string) func(*http.Request) string {
	return func(r *http.Request) string {
		return "/internal/v1/cases/" + url.PathEscape(r.PathValue("caseId")) + suffix
	}
}

// documentTarget addresses a document directly and passes its case id, so
// intake-service can reject a document that belongs to another case.
func documentTarget(suffix string) func(*http.Request) string {
	return func(r *http.Request) string {
		return "/internal/v1/documents/" + url.PathEscape(r.PathValue("documentId")) + suffix +
			"?caseId=" + url.QueryEscape(r.PathValue("caseId"))
	}
}

// Routes registers the public API on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.pattern, s.authenticated(rt.caseworkerOnly, s.forward(rt)))
	}
}

// CORS lets the local API explorer call the gateway from the browser.
func (s *Server) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(s.CORSOrigins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers", "ETag, Location, X-Correlation-Id, Retry-After")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Idempotency-Key, X-Correlation-Id")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// authenticated verifies the bearer token (FR-101), applies the per-subject
// rate limit (FR-107) and the role rule of the route (FR-104).
func (s *Server) authenticated(caseworkerOnly bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			platform.WriteProblem(w, r, platform.NewProblem(platform.CodeUnauthenticated, "a bearer token is required"))
			return
		}
		subject, err := s.Verifier.Verify(r.Context(), raw)
		if err != nil {
			s.Log.InfoContext(r.Context(), "token rejected", "error", err)
			platform.WriteProblem(w, r, platform.NewProblem(platform.CodeUnauthenticated, "missing, malformed or invalid token"))
			return
		}
		if !s.limiter(subject.ID).Allow() {
			w.Header().Set("Retry-After", "1")
			platform.WriteProblem(w, r, platform.NewProblem(platform.CodeRateLimited, "rate limit exceeded"))
			return
		}
		isCaseworker := slices.Contains(subject.Roles, domain.RoleCaseworker)
		allowed := isCaseworker || (!caseworkerOnly && slices.Contains(subject.Roles, domain.RoleCitizen))
		if !allowed {
			platform.WriteProblem(w, r, platform.NewProblem(domain.CodeForbiddenRole, "your roles do not permit this endpoint"))
			return
		}
		next.ServeHTTP(w, r.WithContext(platform.WithSubject(r.Context(), platform.Subject(subject))))
	})
}

func (s *Server) limiter(subject string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limiters == nil {
		s.limiters = map[string]*rate.Limiter{}
	}
	l, ok := s.limiters[subject]
	if !ok {
		l = rate.NewLimiter(s.RateLimit, max(1, int(2*s.RateLimit)))
		s.limiters[subject] = l
	}
	return l
}

// forwarded request and response headers.
var (
	requestHeaders  = []string{"Content-Type", "Accept", "If-Match", platform.HeaderIdempotencyKey}
	responseHeaders = []string{"Content-Type", "ETag", "Location", "Retry-After"}
)

// forward proxies the request to the route's upstream with only the internal
// subject headers, never the token (FR-103).
func (s *Server) forward(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, s.MaxBodyBytes+1))
		if err != nil {
			platform.WriteProblem(w, r, platform.FieldProblem("body", "unreadable request body"))
			return
		}
		if int64(len(body)) > s.MaxBodyBytes {
			platform.WriteProblem(w, r, platform.NewProblem(domain.CodePayloadTooLarge, "request body exceeds %d bytes", s.MaxBodyBytes))
			return
		}
		target := rt.target(r)
		if r.URL.RawQuery != "" {
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			target += sep + r.URL.RawQuery
		}
		header := http.Header{}
		for _, h := range requestHeaders {
			if v := r.Header.Get(h); v != "" {
				header.Set(h, v)
			}
		}
		subject, _ := platform.SubjectFrom(r.Context())
		header.Set(platform.HeaderSubjectID, subject.ID)
		header.Set(platform.HeaderSubjectRoles, strings.Join(subject.Roles, ","))
		header.Set(platform.HeaderAuthLevel, subject.AuthLevel)
		header.Set(platform.HeaderCorrelationID, platform.CorrelationID(r.Context()))
		header.Set(platform.HeaderService, "identity-gateway")

		resp, err := rt.upstream(s).Send(r.Context(), r.Method, target, header, body)
		if err != nil {
			s.Log.WarnContext(r.Context(), "upstream unavailable", "upstream", rt.upstream(s).Name, "error", err)
			platform.WriteProblem(w, r, platform.NewProblem(platform.CodeUpstreamUnavailable, "%s is unavailable", rt.upstream(s).Name))
			return
		}
		defer resp.Body.Close()
		for _, h := range responseHeaders {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

// Upstream is one internal service, with a per-call timeout, bounded retries
// for idempotent methods and a circuit breaker (FR-105).
type Upstream struct {
	Name    string
	Base    string
	Timeout time.Duration
	Client  *http.Client

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const (
	breakerThreshold = 5
	breakerCooldown  = 10 * time.Second
	getAttempts      = 3
)

// errOpen is returned while the circuit breaker is open.
var errOpen = &platform.Problem{Status: http.StatusServiceUnavailable, Code: platform.CodeUpstreamUnavailable, Detail: "circuit open"}

// Send performs the call. Only GET is retried, and only on transport errors
// or 502/503/504.
func (u *Upstream) Send(ctx context.Context, method, target string, header http.Header, body []byte) (*http.Response, error) {
	attempts := 1
	if method == http.MethodGet {
		attempts = getAttempts
	}
	var lastErr error
	for i := range attempts {
		if i > 0 {
			time.Sleep(time.Duration(i) * 100 * time.Millisecond)
		}
		if !u.allow() {
			return nil, errOpen
		}
		cctx, cancel := context.WithTimeout(ctx, u.Timeout)
		req, err := http.NewRequestWithContext(cctx, method, u.Base+target, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header = header.Clone()
		resp, err := u.Client.Do(req)
		retryable := err != nil || resp.StatusCode == http.StatusBadGateway ||
			resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout
		u.record(!retryable)
		if !retryable {
			resp.Body = cancelOnClose{resp.Body, cancel}
			return resp, nil
		}
		if err == nil {
			if i == attempts-1 {
				resp.Body = cancelOnClose{resp.Body, cancel}
				return resp, nil // pass the upstream's own 5xx through
			}
			resp.Body.Close()
		}
		cancel()
		lastErr = err
	}
	return nil, lastErr
}

func (u *Upstream) allow() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return time.Now().After(u.openUntil)
}

func (u *Upstream) record(ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if ok {
		u.failures = 0
		return
	}
	u.failures++
	if u.failures >= breakerThreshold {
		u.openUntil, u.failures = time.Now().Add(breakerCooldown), 0
	}
}

// cancelOnClose releases the per-call context once the body has been read.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}
