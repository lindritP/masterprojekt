package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// CaseType is the kind of application a citizen submits.
type CaseType string

const (
	CaseBuildingPermit        CaseType = "BUILDING_PERMIT"
	CaseResidenceRegistration CaseType = "RESIDENCE_REGISTRATION"
	CaseBenefitClaim          CaseType = "BENEFIT_CLAIM"
)

var caseTypes = []CaseType{CaseBuildingPermit, CaseResidenceRegistration, CaseBenefitClaim}

// ParseCaseType validates a case type from the API.
func ParseCaseType(s string) (CaseType, error) {
	if !slices.Contains(caseTypes, CaseType(s)) {
		return "", &Error{Code: CodeInvalidCaseType, Field: "caseType", Detail: fmt.Sprintf("unknown case type %q", s)}
	}
	return CaseType(s), nil
}

const maxTitleLength = 200

// Decision is a caseworker's verdict on a case under review.
type Decision string

const (
	DecisionAccepted Decision = "ACCEPTED"
	DecisionRejected Decision = "REJECTED"
)

// Case is the aggregate root: a citizen's submission (SRS §3.1).
type Case struct {
	ID             uuid.UUID
	Reference      string
	Type           CaseType
	State          State
	SubjectID      string // immutable (INV-5)
	Title          string
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	SubmittedAt    *time.Time
	DecidedAt      *time.Time
	DecidedBy      string
	DecisionReason string
	Documents      []*Document
}

// CaseSummary is the list projection of a case (SRS §5.3).
type CaseSummary struct {
	ID            uuid.UUID
	Reference     string
	Type          CaseType
	State         State
	Title         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DocumentCount int // active documents only
}

// FormatReference renders the human-readable case reference CASE-<year>-<seq>.
// The sequence number is allocated by the repository.
func FormatReference(year int, seq int64) string {
	return fmt.Sprintf("CASE-%d-%06d", year, seq)
}

// DocumentPolicy holds the configured limits for attaching documents.
type DocumentPolicy struct {
	MaxDocuments int      // CCSRA_MAX_DOCUMENTS_PER_CASE
	MaxBytes     int64    // CCSRA_MAX_OBJECT_BYTES
	AllowedMime  []string // CCSRA_ALLOWED_MIME
}

// NewCase creates a case in DRAFT owned by the acting subject (T1).
func NewCase(id uuid.UUID, reference, caseType, title string, op Op) (*Case, []AuditEntry, error) {
	if op.Actor.Type == ActorSystem || op.Actor.ID == "" {
		return nil, nil, fail(CodeForbiddenRole, "a case must be created by an authenticated subject")
	}
	ct, err := ParseCaseType(caseType)
	if err != nil {
		return nil, nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxTitleLength {
		return nil, nil, invalidField("title", "must be 1 to %d characters", maxTitleLength)
	}

	c := &Case{
		ID:        id,
		Reference: reference,
		Type:      ct,
		State:     StateDraft,
		SubjectID: op.Actor.ID,
		Title:     title,
		Version:   1,
		CreatedAt: op.At,
		UpdatedAt: op.At,
	}
	m := c.mutate(op)
	m.record(EventCaseCreated, map[string]any{"reference": reference, "caseType": string(ct)})
	return c, m.audit, nil
}

// ExpectVersion fails with CONCURRENT_MODIFICATION unless the caller's
// version is current (INV-6, FR-203).
func (c *Case) ExpectVersion(v int) error {
	if v != c.Version {
		return fail(CodeConcurrentModification, "case is at version %d, request carried %d", c.Version, v)
	}
	return nil
}

// Document returns the document with the given id.
func (c *Case) Document(id uuid.UUID) (*Document, error) {
	for _, d := range c.Documents {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, fail(CodeDocumentNotFound, "document %s not found in case %s", id, c.ID)
}

// AttachDocument registers a document in PENDING_UPLOAD (FR-303). In
// NEEDS_INFO, a document with the same filename replaces the active one,
// which becomes SUPERSEDED (FR-211, INV-7).
func (c *Case) AttachDocument(id uuid.UUID, filename, declaredMime string, declaredSize int64, p DocumentPolicy, op Op) (*Document, []AuditEntry, error) {
	if err := c.authorizeOwner(op.Actor); err != nil {
		return nil, nil, err
	}
	if err := c.documentsMutable(); err != nil {
		return nil, nil, err
	}
	if filename == "" || utf8.RuneCountInString(filename) > maxFilenameLength {
		return nil, nil, invalidField("filename", "must be 1 to %d characters", maxFilenameLength)
	}
	if !slices.Contains(p.AllowedMime, declaredMime) {
		return nil, nil, &Error{Code: CodeUnsupportedMediaType, Field: "declaredMime", Detail: fmt.Sprintf("%q is not accepted", declaredMime)}
	}
	if declaredSize <= 0 {
		return nil, nil, invalidField("sizeBytes", "must be positive")
	}
	if declaredSize > p.MaxBytes {
		return nil, nil, fail(CodePayloadTooLarge, "%d bytes exceeds the maximum of %d", declaredSize, p.MaxBytes)
	}

	var replaced *Document
	active := 0
	for _, d := range c.Documents {
		if !d.active() {
			continue
		}
		active++
		if d.Filename == filename {
			replaced = d
		}
	}
	if replaced != nil && c.State != StateNeedsInfo {
		return nil, nil, invalidField("filename", "a document named %q is already attached; delete it first", filename)
	}
	if replaced == nil && active >= p.MaxDocuments {
		return nil, nil, fail(CodeDocumentLimitExceeded, "a case holds at most %d documents", p.MaxDocuments)
	}

	m := c.mutate(op)
	d := &Document{
		ID:           id,
		CaseID:       c.ID,
		Filename:     filename,
		DeclaredMime: declaredMime,
		StorageKey:   StorageKey(c.ID, id),
		Status:       DocPendingUpload,
		CreatedAt:    op.At,
	}
	payload := map[string]any{"documentId": id.String(), "filename": filename}
	if replaced != nil {
		replaced.Status = DocSuperseded
		d.SupersedesID = &replaced.ID
		payload["supersedesId"] = replaced.ID.String()
	}
	c.Documents = append(c.Documents, d)
	m.record(EventDocumentAttached, payload)
	return d, m.done(), nil
}

// CompleteUpload confirms that a document's bytes are in storage (FR-304).
// Repeating it with the same size changes nothing.
func (c *Case) CompleteUpload(documentID uuid.UUID, sizeBytes, maxBytes int64, op Op) ([]AuditEntry, error) {
	if err := c.authorizeOwner(op.Actor); err != nil {
		return nil, err
	}
	d, err := c.Document(documentID)
	if err != nil {
		return nil, err
	}
	if d.Status == DocUploaded && d.SizeBytes == sizeBytes {
		return nil, nil
	}
	if err := c.documentsMutable(); err != nil {
		return nil, err
	}
	if d.Status != DocPendingUpload {
		return nil, fail(CodeInvalidStateTransition, "document is %s; only a PENDING_UPLOAD document can be completed", d.Status)
	}
	if sizeBytes <= 0 {
		return nil, invalidField("sizeBytes", "must be positive")
	}
	if sizeBytes > maxBytes {
		return nil, fail(CodePayloadTooLarge, "%d bytes exceeds the maximum of %d", sizeBytes, maxBytes)
	}

	m := c.mutate(op)
	d.Status = DocUploaded
	d.SizeBytes = sizeBytes
	d.UploadedAt = &op.At
	m.record(EventDocumentUploaded, map[string]any{"documentId": d.ID.String(), "sizeBytes": sizeBytes})
	return m.done(), nil
}

// RemoveDocument marks a document deleted (FR-308). Removing an already
// deleted document changes nothing.
func (c *Case) RemoveDocument(documentID uuid.UUID, op Op) ([]AuditEntry, error) {
	if err := c.authorizeOwner(op.Actor); err != nil {
		return nil, err
	}
	d, err := c.Document(documentID)
	if err != nil {
		return nil, err
	}
	if d.Status == DocDeleted {
		return nil, nil
	}
	if err := c.documentsMutable(); err != nil {
		return nil, err
	}

	m := c.mutate(op)
	d.Status = DocDeleted
	m.record(EventDocumentDeleted, map[string]any{"documentId": d.ID.String()})
	return m.done(), nil
}

// Submit moves the case to VALIDATING (T2 from DRAFT, T3 from NEEDS_INFO).
//
// Uploaded documents usually have been validated already, because the worker
// starts on document.uploaded. Those go straight to VALID or INVALID; the
// rest wait in VALIDATING. If nothing is outstanding, T4 or T5 follows in the
// same operation.
func (c *Case) Submit(op Op) ([]AuditEntry, error) {
	if err := c.authorizeOwner(op.Actor); err != nil {
		return nil, err
	}
	if err := checkAllowed(c.State, TriggerSubmit); err != nil {
		return nil, err
	}
	// In NEEDS_INFO every previously submitted document is VALID or INVALID,
	// so any UPLOADED document necessarily arrived after the transition (T3).
	var uploaded []*Document
	for _, d := range c.Documents {
		if d.Status == DocUploaded {
			uploaded = append(uploaded, d)
		}
	}
	if len(uploaded) == 0 {
		return nil, fail(CodeNoDocumentsAttached, "no document in UPLOADED")
	}

	m := c.mutate(op)
	ids := make([]string, len(uploaded))
	for i, d := range uploaded {
		ids[i] = d.ID.String()
		if d.Result != nil {
			d.settle()
		} else {
			d.Status = DocValidating
		}
	}
	resubmission := c.State == StateNeedsInfo
	if err := m.setState(TriggerSubmit, StateValidating, map[string]any{"documentIds": ids, "resubmission": resubmission}); err != nil {
		return nil, err
	}
	if c.SubmittedAt == nil {
		c.SubmittedAt = &op.At
	}
	if err := m.evaluate(); err != nil {
		return nil, err
	}
	return m.done(), nil
}

// RecordValidation stores the worker's result for one document (FR-206). If
// it was the last outstanding result of a submitted case, T4 or T5 follows in
// the same operation (FR-207). An identical repeat is reported as a replay and
// changes nothing; a different second result fails (INV-8).
func (c *Case) RecordValidation(r ValidationResult, op Op) (audit []AuditEntry, replayed bool, err error) {
	if err := requireType(op.Actor, ActorSystem); err != nil {
		return nil, false, err
	}
	if err := r.Validate(); err != nil {
		return nil, false, err
	}
	d, err := c.Document(r.DocumentID)
	if err != nil {
		return nil, false, err
	}
	if d.Result != nil {
		if d.Result.SameAs(r) {
			return nil, true, nil
		}
		return nil, false, fail(CodeDocumentAlreadyValidated, "document %s already has a different validation result", d.ID)
	}
	if d.Status == DocPendingUpload {
		return nil, false, fail(CodeInvalidStateTransition, "document %s has not been uploaded", d.ID)
	}

	m := c.mutate(op)
	d.Result = &r
	d.ValidatedAt = &op.At
	d.DetectedMime = r.Checks.DetectedMime
	d.SHA256 = r.Checks.SHA256
	if d.Status == DocValidating {
		d.settle()
	}
	m.record(EventDocumentValidated, map[string]any{
		"documentId":   d.ID.String(),
		"outcome":      string(r.Outcome),
		"failureCodes": r.FailureCodes,
	})
	if err := m.evaluate(); err != nil {
		return nil, false, err
	}
	return m.done(), false, nil
}

// Decide records a caseworker's decision on a case under review (T6, T7).
func (c *Case) Decide(decision Decision, reason string, op Op) ([]AuditEntry, error) {
	if err := requireType(op.Actor, ActorCaseworker); err != nil {
		return nil, err
	}
	// Separation of duties: a subject holding both roles may file cases, but
	// never decide one of their own (INV-9).
	if op.Actor.ID == c.SubjectID {
		return nil, fail(CodeForbiddenRole, "a caseworker cannot decide their own case")
	}
	var trigger Trigger
	var to State
	switch decision {
	case DecisionAccepted:
		trigger, to = TriggerAccept, StateAccepted
	case DecisionRejected:
		trigger, to = TriggerReject, StateRejected
	default:
		return nil, invalidField("decision", "must be ACCEPTED or REJECTED, got %q", decision)
	}
	if err := checkAllowed(c.State, trigger); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if decision == DecisionRejected && reason == "" {
		return nil, &Error{Code: CodeDecisionReasonRequired, Field: "reason", Detail: "a rejection needs a reason"}
	}

	m := c.mutate(op)
	if err := m.setState(trigger, to, map[string]any{"decision": string(decision), "reason": reason}); err != nil {
		return nil, err
	}
	c.DecidedAt = &op.At
	c.DecidedBy = op.Actor.ID
	c.DecisionReason = reason
	return m.done(), nil
}

// documentsMutable enforces INV-2.
func (c *Case) documentsMutable() error {
	if c.State != StateDraft && c.State != StateNeedsInfo {
		return fail(CodeDocumentsImmutable, "documents cannot change while the case is %s", c.State)
	}
	return nil
}

// evaluate applies T4 or T5 once a submitted case has no outstanding result.
// Documents still in PENDING_UPLOAD were never submitted and do not count.
func (m *mutation) evaluate() error {
	c := m.c
	if c.State != StateValidating {
		return nil
	}
	anyInvalid := false
	for _, d := range c.Documents {
		if !d.active() || !d.submitted() {
			continue
		}
		switch d.Status {
		case DocValidating:
			return nil
		case DocInvalid:
			anyInvalid = true
		}
	}
	if anyInvalid {
		return m.setState(TriggerValidationComplete, StateNeedsInfo, nil)
	}
	return m.setState(TriggerValidationComplete, StateUnderReview, nil)
}
