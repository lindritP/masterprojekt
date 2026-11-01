// Package caseservice is the internal API of case-service (SRS §4.3, §5.4):
// the only service holding a database connection (FR-005), and the owner of
// case state, document lifecycle and the audit trail.
package caseservice

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/events"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

const source = "/ccsra/case-service"

// Server serves the case-service internal API.
type Server struct {
	Repo         ports.CaseRepository
	Bus          ports.EventBus
	Policy       domain.DocumentPolicy
	MaxBodyBytes int64
	Log          *slog.Logger
}

// Routes registers the internal API (SRS §5.4) on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	h := func(pattern string, fn platform.HandlerFunc) { mux.Handle(pattern, platform.Handle(s.Log, fn)) }
	h("POST /internal/v1/cases", s.createCase)
	h("GET /internal/v1/cases", s.listCases)
	h("GET /internal/v1/cases/{id}", s.getCase)
	h("POST /internal/v1/cases/{id}/submit", s.submit)
	h("POST /internal/v1/cases/{id}/decision", s.decide)
	h("GET /internal/v1/cases/{id}/events", s.listEvents)
	h("GET /internal/v1/cases/{id}/export", s.export)
	h("POST /internal/v1/cases/{id}/documents", s.registerDocument)
	h("PATCH /internal/v1/documents/{id}", s.completeDocument)
	h("DELETE /internal/v1/documents/{id}", s.deleteDocument)
	h("GET /internal/v1/documents/{id}/authz", s.authorizeDocument)
	h("POST /internal/v1/documents/{id}/validation-result", s.recordValidation)
}

// actor is the subject propagated by identity-gateway (SRS §5.1).
func actor(r *http.Request) (domain.Actor, error) {
	s, ok := platform.SubjectFrom(r.Context())
	if !ok {
		return domain.Actor{}, platform.NewProblem(platform.CodeUnauthenticated, "no subject propagated")
	}
	return domain.ActorFor(s.ID, s.Roles), nil
}

func op(r *http.Request, a domain.Actor) domain.Op {
	return domain.Op{Actor: a, CorrelationID: platform.CorrelationID(r.Context()), At: platform.Now()}
}

func pathID(r *http.Request, notFound string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, platform.NewProblem(notFound, "unknown id %q", r.PathValue("id"))
	}
	return id, nil
}

// load reads a case for actor a, enforcing INV-9 for reads.
func (s *Server) load(r *http.Request, id uuid.UUID, a domain.Actor) (*domain.Case, error) {
	c, err := s.Repo.Get(r.Context(), id)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, platform.NewProblem(domain.CodeCaseNotFound, "case %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if err := c.AuthorizeRead(a); err != nil {
		return nil, err
	}
	return c, nil
}

func notFoundAsCase(err error, id uuid.UUID) error {
	if errors.Is(err, ports.ErrNotFound) {
		return platform.NewProblem(domain.CodeCaseNotFound, "case %s not found", id)
	}
	return err
}

func writeCase(w http.ResponseWriter, status int, c *domain.Case) error {
	w.Header().Set("ETag", platform.ETag(c.Version))
	return platform.WriteJSON(w, status, detailOf(c))
}

// POST /internal/v1/cases — T1 (FR-201).
func (s *Server) createCase(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	var in struct {
		CaseType string `json:"caseType"`
		Title    string `json:"title"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	if _, err := domain.ParseCaseType(in.CaseType); err != nil {
		return err
	}
	ref, err := s.Repo.AllocateReference(r.Context(), time.Now().UTC().Year())
	if err != nil {
		return err
	}
	c, audit, err := domain.NewCase(uuid.Must(uuid.NewV7()), ref, in.CaseType, in.Title, op(r, a))
	if err != nil {
		return err
	}
	if err := s.Repo.Create(r.Context(), c, audit); err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/cases/"+c.ID.String())
	return writeCase(w, http.StatusCreated, c)
}

// GET /internal/v1/cases — a citizen sees their own cases, a caseworker all (FR-210).
func (s *Server) listCases(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := ports.ListFilter{After: ports.Cursor(q.Get("cursor"))}
	if a.Type != domain.ActorCaseworker {
		f.SubjectID = a.ID
	}
	if st := q.Get("state"); st != "" {
		if !isState(st) {
			return platform.FieldProblem("state", "unknown state %q", st)
		}
		f.State = domain.State(st)
	}
	if ct := q.Get("caseType"); ct != "" {
		parsed, err := domain.ParseCaseType(ct)
		if err != nil {
			return err
		}
		f.CaseType = parsed
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			return platform.FieldProblem("limit", "limit must be 1 to 100")
		}
		f.Limit = n
	}
	items, next, err := s.Repo.List(r.Context(), f)
	if err != nil {
		return cursorProblem(err)
	}
	out := make([]caseSummary, len(items))
	for i, it := range items {
		out[i] = summaryOf(it)
	}
	return platform.WriteJSON(w, http.StatusOK, newPage(out, string(next)))
}

// GET /internal/v1/cases/{id}
func (s *Server) getCase(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	c, err := s.load(r, id, a)
	if err != nil {
		return err
	}
	return writeCase(w, http.StatusOK, c)
}

// POST /internal/v1/cases/{id}/submit — T2 / T3, possibly followed by T4 / T5.
func (s *Server) submit(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	version, err := platform.IfMatch(r)
	if err != nil {
		return err
	}
	key, err := platform.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var after *domain.Case
	var resubmission bool
	var submittedIDs []string
	replayed, err := s.Repo.TransitionOnce(r.Context(), "submit:"+id.String(), key, id, version, func(c *domain.Case) ([]domain.AuditEntry, error) {
		resubmission = c.State == domain.StateNeedsInfo
		for _, d := range c.Documents {
			if d.Status == domain.DocUploaded {
				submittedIDs = append(submittedIDs, d.ID.String())
			}
		}
		audit, err := c.Submit(op(r, a))
		after = c
		return audit, err
	})
	if err != nil {
		return notFoundAsCase(err, id)
	}
	if replayed {
		if after, err = s.load(r, id, a); err != nil {
			return err
		}
	} else {
		s.publish(r, events.TypeCaseSubmitted, after.ID, events.CaseSubmitted{
			CaseID: after.ID.String(), Reference: after.Reference, CaseType: string(after.Type), SubjectID: after.SubjectID,
			DocumentIDs: submittedIDs, SubmittedAt: platform.Millis(*after.SubmittedAt), Resubmission: resubmission,
		})
	}
	w.Header().Set("ETag", platform.ETag(after.Version))
	return platform.WriteJSON(w, http.StatusAccepted, map[string]any{"id": after.ID.String(), "state": after.State, "version": after.Version})
}

// POST /internal/v1/cases/{id}/decision — T6 / T7.
func (s *Server) decide(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	version, err := platform.IfMatch(r)
	if err != nil {
		return err
	}
	key, err := platform.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var after *domain.Case
	replayed, err := s.Repo.TransitionOnce(r.Context(), "decide:"+id.String(), key, id, version, func(c *domain.Case) ([]domain.AuditEntry, error) {
		audit, err := c.Decide(domain.Decision(in.Decision), in.Reason, op(r, a))
		after = c
		return audit, err
	})
	if err != nil {
		return notFoundAsCase(err, id)
	}
	if replayed {
		if after, err = s.load(r, id, a); err != nil {
			return err
		}
	} else {
		s.publish(r, events.TypeCaseDecided, after.ID, events.CaseDecided{
			CaseID: after.ID.String(), Reference: after.Reference, Decision: string(after.State),
			Reason: after.DecisionReason, DecidedBy: after.DecidedBy, DecidedAt: platform.Millis(*after.DecidedAt),
		})
	}
	w.Header().Set("ETag", platform.ETag(after.Version))
	return platform.WriteJSON(w, http.StatusOK, map[string]any{
		"id": after.ID.String(), "state": after.State, "decidedAt": platform.MillisPtr(after.DecidedAt), "version": after.Version,
	})
}

// GET /internal/v1/cases/{id}/events — the audit trail (FR-212).
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	if _, err := s.load(r, id, a); err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := s.Repo.ListAudit(r.Context(), id, limit, ports.Cursor(r.URL.Query().Get("cursor")))
	if err != nil {
		return cursorProblem(err)
	}
	out := make([]auditEntry, len(items))
	for i, it := range items {
		out[i] = auditOf(it)
	}
	return platform.WriteJSON(w, http.StatusOK, newPage(out, string(next)))
}

// GET /internal/v1/cases/{id}/export — case and audit trail as JSON Lines,
// caseworkers only (FR-214).
func (s *Server) export(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	if a.Type != domain.ActorCaseworker {
		return platform.NewProblem(domain.CodeForbiddenRole, "export is for caseworkers")
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	c, err := s.load(r, id, a)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	_ = enc.Encode(map[string]any{"kind": "case", "case": detailOf(c)})
	for cursor := ports.Cursor(""); ; {
		items, next, err := s.Repo.ListAudit(r.Context(), id, 100, cursor)
		if err != nil {
			s.Log.ErrorContext(r.Context(), "export aborted", "error", err)
			break
		}
		for _, it := range items {
			_ = enc.Encode(map[string]any{"kind": "audit", "entry": auditOf(it)})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return bw.Flush()
}

// POST /internal/v1/cases/{id}/documents — register a document in
// PENDING_UPLOAD for intake-service (FR-303).
func (s *Server) registerDocument(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, domain.CodeCaseNotFound)
	if err != nil {
		return err
	}
	var in struct {
		Filename     string `json:"filename"`
		DeclaredMime string `json:"declaredMime"`
		SizeBytes    int64  `json:"sizeBytes"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	var doc *domain.Document
	err = s.Repo.Transition(r.Context(), id, 0, func(c *domain.Case) ([]domain.AuditEntry, error) {
		d, audit, err := c.AttachDocument(uuid.Must(uuid.NewV7()), in.Filename, in.DeclaredMime, in.SizeBytes, s.Policy, op(r, a))
		doc = d
		return audit, err
	})
	if err != nil {
		return notFoundAsCase(err, id)
	}
	return platform.WriteJSON(w, http.StatusCreated, documentRef(id, doc))
}

// PATCH /internal/v1/documents/{id} — confirm an upload (FR-304).
func (s *Server) completeDocument(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	docID, caseID, err := s.documentCase(r)
	if err != nil {
		return err
	}
	var in struct {
		SizeBytes int64 `json:"sizeBytes"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	var doc *domain.Document
	err = s.Repo.Transition(r.Context(), caseID, 0, func(c *domain.Case) ([]domain.AuditEntry, error) {
		audit, err := c.CompleteUpload(docID, in.SizeBytes, s.Policy.MaxBytes, op(r, a))
		doc, _ = c.Document(docID)
		return audit, err
	})
	if err != nil {
		return err
	}
	return platform.WriteJSON(w, http.StatusOK, documentRef(caseID, doc))
}

// DELETE /internal/v1/documents/{id} — FR-308; requires If-Match (case version).
func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	docID, caseID, err := s.documentCase(r)
	if err != nil {
		return err
	}
	version, err := platform.IfMatch(r)
	if err != nil {
		return err
	}
	err = s.Repo.Transition(r.Context(), caseID, version, func(c *domain.Case) ([]domain.AuditEntry, error) {
		return c.RemoveDocument(docID, op(r, a))
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// GET /internal/v1/documents/{id}/authz?action=read|mutate — may the subject
// access this document? Used by intake-service before it touches storage.
func (s *Server) authorizeDocument(w http.ResponseWriter, r *http.Request) error {
	a, err := actor(r)
	if err != nil {
		return err
	}
	docID, caseID, err := s.documentCase(r)
	if err != nil {
		return err
	}
	c, err := s.Repo.Get(r.Context(), caseID)
	if err != nil {
		return err
	}
	switch r.URL.Query().Get("action") {
	case "mutate":
		err = c.AuthorizeMutation(a)
	default:
		err = c.AuthorizeRead(a)
	}
	if domain.CodeOf(err) == domain.CodeCaseNotFound {
		return platform.NewProblem(domain.CodeDocumentNotFound, "document %s not found", docID)
	}
	if err != nil {
		return err
	}
	d, err := c.Document(docID)
	if err != nil {
		return err
	}
	return platform.WriteJSON(w, http.StatusOK, documentRef(caseID, d))
}

// POST /internal/v1/documents/{id}/validation-result — FR-206, called by
// validation-worker only, idempotent on Idempotency-Key (FR-403).
func (s *Server) recordValidation(w http.ResponseWriter, r *http.Request) error {
	if svc := platform.CallerService(r.Context()); svc != "validation-worker" {
		return platform.NewProblem(domain.CodeForbiddenRole, "only validation-worker reports validation results")
	}
	docID, err := pathID(r, domain.CodeDocumentNotFound)
	if err != nil {
		return err
	}
	key, err := platform.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var in struct {
		Outcome       string        `json:"outcome"`
		FailureCodes  []string      `json:"failureCodes"`
		Checks        domain.Checks `json:"checks"`
		WorkerVersion string        `json:"workerVersion"`
		DurationMs    int           `json:"durationMs"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	res := domain.ValidationResult{
		DocumentID: docID, Outcome: domain.Outcome(in.Outcome), Checks: in.Checks,
		WorkerVersion: in.WorkerVersion, DurationMs: in.DurationMs,
	}
	if len(in.FailureCodes) > 0 {
		res.FailureCodes = in.FailureCodes
	}
	replayed, err := s.Repo.RecordValidationResult(r.Context(), key, res, op(r, domain.System("validation-worker")))
	if err != nil {
		return err
	}
	return platform.WriteJSON(w, http.StatusOK, map[string]any{"documentId": docID.String(), "replayed": replayed})
}

// documentCase resolves {id} to a document and its case.
func (s *Server) documentCase(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	docID, err := pathID(r, domain.CodeDocumentNotFound)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	caseID, err := s.Repo.CaseIDOfDocument(r.Context(), docID)
	if errors.Is(err, ports.ErrNotFound) {
		return uuid.Nil, uuid.Nil, platform.NewProblem(domain.CodeDocumentNotFound, "document %s not found", docID)
	}
	return docID, caseID, err
}

// documentRef is what intake-service needs to know about a document.
func documentRef(caseID uuid.UUID, d *domain.Document) map[string]any {
	out := map[string]any{
		"caseId": caseID.String(), "documentId": d.ID.String(), "storageKey": d.StorageKey,
		"filename": d.Filename, "declaredMime": d.DeclaredMime, "status": d.Status,
	}
	if d.SizeBytes > 0 {
		out["sizeBytes"] = d.SizeBytes
	}
	if d.UploadedAt != nil {
		out["uploadedAt"] = platform.Millis(*d.UploadedAt)
	}
	return out
}

// publish emits an event after a commit. A lost event is accepted and logged,
// not retried here (ADR 0005).
func (s *Server) publish(r *http.Request, eventType string, caseID uuid.UUID, data any) {
	if err := events.Publish(r.Context(), s.Bus, eventType, source, caseID.String(), data); err != nil {
		s.Log.ErrorContext(r.Context(), "event lost after commit", "type", eventType, "caseId", caseID, "error", err)
	}
}

// cursorProblem reports an undecodable cursor as a client error. Any other
// failure stays an internal error, logged but not described to the caller.
func cursorProblem(err error) error {
	if errors.Is(err, ports.ErrBadCursor) {
		return platform.FieldProblem("cursor", "invalid cursor")
	}
	return err
}

func isState(s string) bool {
	for _, st := range domain.States {
		if string(st) == s {
			return true
		}
	}
	return false
}
