package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	alice  = Actor{Type: ActorCitizen, ID: "alice"}
	bob    = Actor{Type: ActorCitizen, ID: "bob"}
	carol  = Actor{Type: ActorCaseworker, ID: "carol"}
	worker = System("validation-worker")

	policy = DocumentPolicy{MaxDocuments: 3, MaxBytes: 20 << 20, AllowedMime: []string{"application/pdf"}}

	clockBase = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	clockTick int
)

// opBy returns an operation by a, one second after the previous one.
func opBy(a Actor) Op {
	clockTick++
	return Op{Actor: a, CorrelationID: "corr-test", At: clockBase.Add(time.Duration(clockTick) * time.Second)}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := CodeOf(err); got != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func newDraft(t *testing.T) *Case {
	t.Helper()
	c, _, err := NewCase(uuid.New(), "CASE-2026-000001", string(CaseBuildingPermit), "Garage extension", opBy(alice))
	must(t, err)
	return c
}

// uploaded attaches a document and completes its upload, as its owner.
func uploaded(t *testing.T, c *Case, filename string) *Document {
	t.Helper()
	d, _, err := c.AttachDocument(uuid.New(), filename, "application/pdf", 1000, policy, opBy(alice))
	must(t, err)
	_, err = c.CompleteUpload(d.ID, 1000, policy.MaxBytes, opBy(alice))
	must(t, err)
	return d
}

func passResult(d *Document) ValidationResult {
	return ValidationResult{DocumentID: d.ID, Outcome: OutcomePass, Checks: Checks{DetectedMime: "application/pdf", SizeBytes: 1000, SHA256: "aa", PageCount: 1}, WorkerVersion: "test", DurationMs: 12}
}

func failResult(d *Document, code string) ValidationResult {
	r := passResult(d)
	r.Outcome, r.FailureCodes = OutcomeFail, []string{code}
	return r
}

func record(t *testing.T, c *Case, r ValidationResult) []AuditEntry {
	t.Helper()
	audit, _, err := c.RecordValidation(r, opBy(worker))
	must(t, err)
	return audit
}

// caseIn builds a case in state s through legitimate operations only. Where
// s has an outgoing submit, its guard is satisfied.
func caseIn(t *testing.T, s State) *Case {
	t.Helper()
	c := newDraft(t)
	d := uploaded(t, c, "plan.pdf")
	if s == StateDraft {
		return c
	}
	_, err := c.Submit(opBy(alice))
	must(t, err)
	switch s {
	case StateValidating:
		return c
	case StateNeedsInfo:
		record(t, c, failResult(d, "CORRUPT_DOCUMENT"))
		uploaded(t, c, "plan.pdf") // replacement, satisfies the T3 guard
		return c
	}
	record(t, c, passResult(d))
	switch s {
	case StateUnderReview:
	case StateAccepted:
		_, err = c.Decide(DecisionAccepted, "", opBy(carol))
	case StateRejected:
		_, err = c.Decide(DecisionRejected, "incomplete plans", opBy(carol))
	}
	must(t, err)
	if c.State != s {
		t.Fatalf("caseIn(%s) produced %s", s, c.State)
	}
	return c
}

func stateChanges(audit []AuditEntry) int {
	n := 0
	for _, a := range audit {
		if a.EventType == EventCaseStateChanged {
			n++
		}
	}
	return n
}
