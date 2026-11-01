package domain

import (
	"time"

	"github.com/google/uuid"
)

// Audit event types (SRS §3.1, AuditEvent.eventType).
const (
	EventCaseCreated       = "case.created"
	EventCaseStateChanged  = "case.state_changed"
	EventDocumentAttached  = "document.attached"
	EventDocumentUploaded  = "document.uploaded"
	EventDocumentDeleted   = "document.deleted"
	EventDocumentValidated = "document.validated"
)

// AuditEntry is one append-only audit record. The repository assigns its id
// and writes it in the same transaction as the change it describes (INV-3).
type AuditEntry struct {
	ID            int64 // assigned by the repository on write
	CaseID        uuid.UUID
	OccurredAt    time.Time
	ActorType     ActorType
	ActorID       string
	EventType     string
	CorrelationID string
	Payload       map[string]any
}

// Op carries who performs a mutation, when, and under which correlation id
// (FR-014).
type Op struct {
	Actor         Actor
	CorrelationID string
	At            time.Time
}

// mutation collects the audit entries of one operation on a case and applies
// the version increment once at the end (INV-6).
type mutation struct {
	c     *Case
	op    Op
	audit []AuditEntry
}

func (c *Case) mutate(op Op) *mutation { return &mutation{c: c, op: op} }

func (m *mutation) record(eventType string, payload map[string]any) {
	m.audit = append(m.audit, AuditEntry{
		CaseID:        m.c.ID,
		OccurredAt:    m.op.At,
		ActorType:     m.op.Actor.Type,
		ActorID:       m.op.Actor.ID,
		EventType:     eventType,
		CorrelationID: m.op.CorrelationID,
		Payload:       payload,
	})
}

// setState applies a transition from the §3.2 table and records exactly one
// state-change entry for it (INV-3).
func (m *mutation) setState(t Trigger, to State, payload map[string]any) error {
	tr, ok := lookup(m.c.State, t, to)
	if !ok {
		return fail(CodeInvalidStateTransition, "no transition from %s to %s on %s", m.c.State, to, t)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["transition"] = tr.ID
	payload["from"] = string(tr.From)
	payload["to"] = string(tr.To)
	m.c.State = to
	m.record(EventCaseStateChanged, payload)
	return nil
}

func (m *mutation) done() []AuditEntry {
	m.c.Version++
	m.c.UpdatedAt = m.op.At
	return m.audit
}
