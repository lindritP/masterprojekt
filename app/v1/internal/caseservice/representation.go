package caseservice

import (
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
)

// JSON representations of SRS §5.3.

type caseSummary struct {
	ID            string `json:"id"`
	Reference     string `json:"reference"`
	CaseType      string `json:"caseType"`
	State         string `json:"state"`
	Title         string `json:"title"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	DocumentCount int    `json:"documentCount"`
}

type caseDetail struct {
	caseSummary
	Version        int        `json:"version"`
	SubmittedAt    *string    `json:"submittedAt"`
	DecidedAt      *string    `json:"decidedAt"`
	DecidedBy      *string    `json:"decidedBy"`
	DecisionReason *string    `json:"decisionReason"`
	Documents      []document `json:"documents"`
}

type document struct {
	ID           string      `json:"id"`
	Filename     string      `json:"filename"`
	DeclaredMime string      `json:"declaredMime"`
	DetectedMime *string     `json:"detectedMime"`
	SizeBytes    *int64      `json:"sizeBytes"`
	SHA256       *string     `json:"sha256"`
	Status       string      `json:"status"`
	SupersedesID *string     `json:"supersedesId"`
	CreatedAt    string      `json:"createdAt"`
	UploadedAt   *string     `json:"uploadedAt"`
	ValidatedAt  *string     `json:"validatedAt"`
	Validation   *validation `json:"validation"`
}

type validation struct {
	Outcome      string        `json:"outcome"`
	FailureCodes []string      `json:"failureCodes"`
	Checks       domain.Checks `json:"checks"`
}

type auditEntry struct {
	ID            int64          `json:"id"`
	OccurredAt    string         `json:"occurredAt"`
	ActorType     string         `json:"actorType"`
	ActorID       string         `json:"actorId"`
	EventType     string         `json:"eventType"`
	CorrelationID string         `json:"correlationId"`
	Payload       map[string]any `json:"payload"`
}

type page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func newPage[T any](items []T, next string) page[T] {
	if items == nil {
		items = []T{}
	}
	p := page[T]{Items: items}
	if next != "" {
		p.NextCursor = &next
	}
	return p
}

func summaryOf(s domain.CaseSummary) caseSummary {
	return caseSummary{
		ID: s.ID.String(), Reference: s.Reference, CaseType: string(s.Type), State: string(s.State),
		Title: s.Title, CreatedAt: platform.Millis(s.CreatedAt), UpdatedAt: platform.Millis(s.UpdatedAt),
		DocumentCount: s.DocumentCount,
	}
}

func detailOf(c *domain.Case) caseDetail {
	d := caseDetail{
		caseSummary: summaryOf(domain.CaseSummary{
			ID: c.ID, Reference: c.Reference, Type: c.Type, State: c.State, Title: c.Title,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, DocumentCount: activeDocuments(c),
		}),
		Version:        c.Version,
		SubmittedAt:    platform.MillisPtr(c.SubmittedAt),
		DecidedAt:      platform.MillisPtr(c.DecidedAt),
		DecidedBy:      optional(c.DecidedBy),
		DecisionReason: optional(c.DecisionReason),
		Documents:      []document{},
	}
	for _, doc := range c.Documents {
		d.Documents = append(d.Documents, documentOf(doc))
	}
	return d
}

func documentOf(d *domain.Document) document {
	out := document{
		ID: d.ID.String(), Filename: d.Filename, DeclaredMime: d.DeclaredMime,
		DetectedMime: optional(d.DetectedMime), SHA256: optional(d.SHA256), Status: string(d.Status),
		CreatedAt: platform.Millis(d.CreatedAt), UploadedAt: platform.MillisPtr(d.UploadedAt),
		ValidatedAt: platform.MillisPtr(d.ValidatedAt),
	}
	if d.SizeBytes > 0 {
		out.SizeBytes = &d.SizeBytes
	}
	if d.SupersedesID != nil {
		s := d.SupersedesID.String()
		out.SupersedesID = &s
	}
	if d.Result != nil {
		codes := d.Result.FailureCodes
		if codes == nil {
			codes = []string{}
		}
		out.Validation = &validation{Outcome: string(d.Result.Outcome), FailureCodes: codes, Checks: d.Result.Checks}
	}
	return out
}

func auditOf(a domain.AuditEntry) auditEntry {
	payload := a.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return auditEntry{
		ID: a.ID, OccurredAt: platform.Millis(a.OccurredAt), ActorType: string(a.ActorType), ActorID: a.ActorID,
		EventType: a.EventType, CorrelationID: a.CorrelationID, Payload: payload,
	}
}

func activeDocuments(c *domain.Case) int {
	n := 0
	for _, d := range c.Documents {
		if d.Status != domain.DocSuperseded && d.Status != domain.DocDeleted {
			n++
		}
	}
	return n
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
