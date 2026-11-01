// Package intake is the internal API of intake-service (SRS §4.4, §5.5): the
// only service linking an object-storage SDK (FR-300). It never handles
// document bytes; clients and validation-worker use presigned URLs (FR-301,
// FR-004).
package intake

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/events"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

const source = "/ccsra/intake-service"

// Server serves the intake-service internal API.
type Server struct {
	Cases          *platform.Client    // case-service internal API
	Public         ports.DocumentStore // signs URLs for clients outside the cluster
	Internal       ports.DocumentStore // direct calls and URLs for validation-worker
	Bus            ports.EventBus
	MaxObjectBytes int64
	AllowedMime    []string
	PresignTTL     time.Duration
	MaxBodyBytes   int64
	Log            *slog.Logger
}

// Routes registers the internal API (SRS §5.5) on mux.
func (s *Server) Routes(mux *http.ServeMux) {
	h := func(pattern string, fn platform.HandlerFunc) { mux.Handle(pattern, platform.Handle(s.Log, fn)) }
	h("POST /internal/v1/cases/{caseId}/documents", s.register)
	h("POST /internal/v1/documents/{id}/complete", s.complete)
	h("GET /internal/v1/documents/{id}/download-url", s.downloadURL)
	h("DELETE /internal/v1/documents/{id}", s.delete)
	h("POST /internal/v1/objects/presign", s.presignForWorker)
}

// docRef is case-service's view of a document.
type docRef struct {
	CaseID       string `json:"caseId"`
	DocumentID   string `json:"documentId"`
	StorageKey   string `json:"storageKey"`
	Filename     string `json:"filename"`
	DeclaredMime string `json:"declaredMime"`
	Status       string `json:"status"`
	SizeBytes    int64  `json:"sizeBytes"`
	UploadedAt   string `json:"uploadedAt"`
}

// POST /internal/v1/cases/{caseId}/documents — register, then issue a
// presigned upload constrained to the declared type and size (FR-301–FR-303).
func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Filename     string `json:"filename"`
		DeclaredMime string `json:"declaredMime"`
		SizeBytes    int64  `json:"sizeBytes"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	if !slices.Contains(s.AllowedMime, in.DeclaredMime) {
		return platform.NewProblem(domain.CodeUnsupportedMediaType, "%q is not accepted", in.DeclaredMime)
	}
	if in.SizeBytes > s.MaxObjectBytes {
		return platform.NewProblem(domain.CodePayloadTooLarge, "%d bytes exceeds the maximum of %d", in.SizeBytes, s.MaxObjectBytes)
	}

	var ref docRef
	if _, err := s.Cases.Do(r.Context(), http.MethodPost, "/internal/v1/cases/"+url.PathEscape(r.PathValue("caseId"))+"/documents", nil, in, &ref); err != nil {
		return err
	}
	up, err := s.Public.PresignUpload(r.Context(), ref.StorageKey, ports.PutConstraints{
		ContentType: in.DeclaredMime, MaxSizeBytes: in.SizeBytes, TTL: s.PresignTTL,
	})
	if err != nil {
		return err
	}
	headers := up.RequiredHeaders
	if headers == nil {
		headers = map[string]string{}
	}
	fields := up.FormFields
	if fields == nil {
		fields = map[string]string{}
	}
	return platform.WriteJSON(w, http.StatusCreated, map[string]any{
		"documentId": ref.DocumentID, "uploadUrl": up.URL, "uploadMethod": up.Method,
		"requiredHeaders": headers, "formFields": fields, "expiresAt": platform.Millis(up.ExpiresAt),
	})
}

// POST /internal/v1/documents/{id}/complete — verify the object, mark the
// document uploaded and publish document.uploaded (FR-304, FR-305).
func (s *Server) complete(w http.ResponseWriter, r *http.Request) error {
	if _, err := platform.IdempotencyKey(r); err != nil {
		return err
	}
	ref, err := s.authorize(r, "mutate")
	if err != nil {
		return err
	}
	info, err := s.Internal.Head(r.Context(), ref.StorageKey)
	if errors.Is(err, ports.ErrNotFound) {
		return platform.NewProblem(platform.CodeUploadNotCompleted, "no object has been uploaded for document %s", ref.DocumentID)
	}
	if err != nil {
		return err
	}
	if info.SizeBytes > s.MaxObjectBytes {
		return platform.NewProblem(domain.CodePayloadTooLarge, "uploaded object of %d bytes exceeds the maximum of %d", info.SizeBytes, s.MaxObjectBytes)
	}
	var done docRef
	if _, err := s.Cases.Do(r.Context(), http.MethodPatch, "/internal/v1/documents/"+url.PathEscape(ref.DocumentID), nil,
		map[string]int64{"sizeBytes": info.SizeBytes}, &done); err != nil {
		return err
	}
	// Published on every successful completion, including a repeat: consumers
	// are idempotent (FR-023), and a repeat heals an event lost after commit.
	if err := events.Publish(r.Context(), s.Bus, events.TypeDocumentUploaded, source, done.CaseID, events.DocumentUploaded{
		CaseID: done.CaseID, DocumentID: done.DocumentID, StorageKey: done.StorageKey, Filename: done.Filename,
		DeclaredMime: done.DeclaredMime, SizeBytes: done.SizeBytes, UploadedAt: done.UploadedAt,
	}); err != nil {
		s.Log.ErrorContext(r.Context(), "event lost after commit", "type", events.TypeDocumentUploaded, "documentId", done.DocumentID, "error", err)
	}
	return platform.WriteJSON(w, http.StatusOK, map[string]any{"documentId": done.DocumentID, "status": done.Status, "sizeBytes": done.SizeBytes})
}

// GET /internal/v1/documents/{id}/download-url — FR-306.
func (s *Server) downloadURL(w http.ResponseWriter, r *http.Request) error {
	ref, err := s.authorize(r, "read")
	if err != nil {
		return err
	}
	switch domain.DocumentStatus(ref.Status) {
	case domain.DocPendingUpload:
		return platform.NewProblem(platform.CodeUploadNotCompleted, "document %s has not been uploaded", ref.DocumentID)
	case domain.DocDeleted:
		return platform.NewProblem(domain.CodeDocumentNotFound, "document %s was deleted", ref.DocumentID)
	}
	u, err := s.Public.PresignGet(r.Context(), ref.StorageKey, s.PresignTTL)
	if err != nil {
		return err
	}
	return platform.WriteJSON(w, http.StatusOK, map[string]string{"downloadUrl": u.URL, "expiresAt": platform.Millis(u.ExpiresAt)})
}

// DELETE /internal/v1/documents/{id} — mark deleted first (case-service
// enforces INV-2 and If-Match), then remove the object (FR-308).
func (s *Server) delete(w http.ResponseWriter, r *http.Request) error {
	ref, err := s.authorize(r, "mutate")
	if err != nil {
		return err
	}
	header := http.Header{"If-Match": {r.Header.Get("If-Match")}}
	if _, err := s.Cases.Do(r.Context(), http.MethodDelete, "/internal/v1/documents/"+url.PathEscape(ref.DocumentID), header, nil, nil); err != nil {
		return err
	}
	if err := s.Internal.Delete(r.Context(), ref.StorageKey); err != nil {
		// The document is already marked deleted; the orphaned object is
		// logged rather than failing the request.
		s.Log.WarnContext(r.Context(), "object not deleted", "storageKey", ref.StorageKey, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// The only objects validation-worker may touch (SRS §5.8). X-Service-Name is
// trusted on the same basis as X-Subject-*: the internal API is reachable only
// inside the cluster (§5.1). The key patterns limit what a compromised pod in
// the cluster could do with a spoofed header: it can read originals, but never
// overwrite one.
var (
	originalKey  = regexp.MustCompile(`^cases/[0-9a-f-]{36}/documents/[0-9a-f-]{36}/original$`)
	thumbnailKey = regexp.MustCompile(`^cases/[0-9a-f-]{36}/documents/[0-9a-f-]{36}/thumbnail\.png$`)
)

const thumbnailMaxBytes = 1 << 20

// POST /internal/v1/objects/presign — short-lived URLs for validation-worker,
// scoped to one object key (FR-307). Signed for the internal endpoint.
func (s *Server) presignForWorker(w http.ResponseWriter, r *http.Request) error {
	if platform.CallerService(r.Context()) != "validation-worker" {
		return platform.NewProblem(domain.CodeForbiddenRole, "presigning for objects is reserved for validation-worker")
	}
	var in struct {
		Key          string `json:"key"`
		Operation    string `json:"operation"`
		TTLSeconds   int    `json:"ttlSeconds"`
		ContentType  string `json:"contentType"`
		MaxSizeBytes int64  `json:"maxSizeBytes"`
	}
	if err := platform.DecodeJSON(w, r, s.MaxBodyBytes, &in); err != nil {
		return err
	}
	ttl := time.Duration(in.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	var u ports.PresignedURL
	var err error
	switch in.Operation {
	case "GET":
		if !originalKey.MatchString(in.Key) {
			return platform.FieldProblem("key", "the worker may only read document originals")
		}
		u, err = s.Internal.PresignGet(r.Context(), in.Key, ttl)
	case "UPLOAD":
		if !thumbnailKey.MatchString(in.Key) || in.ContentType != "image/png" {
			return platform.FieldProblem("key", "the worker may only write PNG thumbnails")
		}
		if in.MaxSizeBytes <= 0 || in.MaxSizeBytes > thumbnailMaxBytes {
			return platform.FieldProblem("maxSizeBytes", "a thumbnail is 1..%d bytes", thumbnailMaxBytes)
		}
		u, err = s.Internal.PresignUpload(r.Context(), in.Key, ports.PutConstraints{ContentType: in.ContentType, MaxSizeBytes: in.MaxSizeBytes, TTL: ttl})
	default:
		return platform.FieldProblem("operation", "operation must be GET or UPLOAD")
	}
	if err != nil {
		return err
	}
	return platform.WriteJSON(w, http.StatusOK, map[string]any{
		"url": u.URL, "method": u.Method, "requiredHeaders": u.RequiredHeaders, "formFields": u.FormFields,
		"expiresAt": platform.Millis(u.ExpiresAt),
	})
}

// authorize asks case-service whether the subject may read or change the
// document, and checks it belongs to the case named in ?caseId= (if given).
func (s *Server) authorize(r *http.Request, action string) (docRef, error) {
	var ref docRef
	path := "/internal/v1/documents/" + url.PathEscape(r.PathValue("id")) + "/authz?action=" + action
	if _, err := s.Cases.Do(r.Context(), http.MethodGet, path, nil, nil, &ref); err != nil {
		return docRef{}, err
	}
	if c := r.URL.Query().Get("caseId"); c != "" && c != ref.CaseID {
		return docRef{}, platform.NewProblem(domain.CodeDocumentNotFound, "document %s is not part of case %s", ref.DocumentID, c)
	}
	return ref, nil
}
