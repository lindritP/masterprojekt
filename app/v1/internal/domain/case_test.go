package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestAttachDocument_Limits(t *testing.T) {
	c := newDraft(t)
	_, _, err := c.AttachDocument(uuid.New(), "a.png", "image/png", 10, policy, opBy(alice))
	wantCode(t, err, CodeUnsupportedMediaType)
	_, _, err = c.AttachDocument(uuid.New(), "big.pdf", "application/pdf", policy.MaxBytes+1, policy, opBy(alice))
	wantCode(t, err, CodePayloadTooLarge)
	_, _, err = c.AttachDocument(uuid.New(), "", "application/pdf", 10, policy, opBy(alice))
	wantCode(t, err, CodeValidationFailed)

	for _, name := range []string{"1.pdf", "2.pdf", "3.pdf"} {
		uploaded(t, c, name)
	}
	_, _, err = c.AttachDocument(uuid.New(), "4.pdf", "application/pdf", 10, policy, opBy(alice))
	wantCode(t, err, CodeDocumentLimitExceeded)

	// Deleted documents no longer count towards the limit.
	_, err = c.RemoveDocument(c.Documents[0].ID, opBy(alice))
	must(t, err)
	_, _, err = c.AttachDocument(uuid.New(), "4.pdf", "application/pdf", 10, policy, opBy(alice))
	must(t, err)
}

func TestCompleteUpload_IsIdempotentAndChecksSize(t *testing.T) {
	c := newDraft(t)
	d, _, err := c.AttachDocument(uuid.New(), "a.pdf", "application/pdf", 10, policy, opBy(alice))
	must(t, err)

	_, err = c.CompleteUpload(d.ID, policy.MaxBytes+1, policy.MaxBytes, opBy(alice))
	wantCode(t, err, CodePayloadTooLarge)
	_, err = c.CompleteUpload(d.ID, 0, policy.MaxBytes, opBy(alice))
	wantCode(t, err, CodeValidationFailed)

	_, err = c.CompleteUpload(d.ID, 500, policy.MaxBytes, opBy(alice))
	must(t, err)
	version := c.Version
	audit, err := c.CompleteUpload(d.ID, 500, policy.MaxBytes, opBy(alice))
	must(t, err)
	if audit != nil || c.Version != version {
		t.Error("repeating a completion must change nothing")
	}
	_, err = c.CompleteUpload(d.ID, 501, policy.MaxBytes, opBy(alice))
	wantCode(t, err, CodeInvalidStateTransition)

	_, err = c.CompleteUpload(uuid.New(), 1, policy.MaxBytes, opBy(alice))
	wantCode(t, err, CodeDocumentNotFound)
}

func TestRemoveDocument_IsIdempotent(t *testing.T) {
	c := newDraft(t)
	d := uploaded(t, c, "a.pdf")
	_, err := c.RemoveDocument(d.ID, opBy(alice))
	must(t, err)
	version := c.Version
	audit, err := c.RemoveDocument(d.ID, opBy(alice))
	must(t, err)
	if audit != nil || c.Version != version || d.Status != DocDeleted {
		t.Error("removing twice must change nothing")
	}
}

func TestRecordValidation_RejectsBadResultsAndPendingDocuments(t *testing.T) {
	c := newDraft(t)
	pending, _, err := c.AttachDocument(uuid.New(), "a.pdf", "application/pdf", 10, policy, opBy(alice))
	must(t, err)
	_, _, err = c.RecordValidation(passResult(pending), opBy(worker))
	wantCode(t, err, CodeInvalidStateTransition)

	bad := passResult(pending)
	bad.FailureCodes = []string{"MIME_MISMATCH"}
	_, _, err = c.RecordValidation(bad, opBy(worker))
	wantCode(t, err, CodeValidationFailed)

	_, _, err = c.RecordValidation(ValidationResult{DocumentID: uuid.New(), Outcome: OutcomePass, WorkerVersion: "x"}, opBy(worker))
	wantCode(t, err, CodeDocumentNotFound)
}

func TestRecordValidation_CopiesWorkerFindingsToDocument(t *testing.T) {
	c := caseIn(t, StateValidating)
	d := c.Documents[0]
	record(t, c, passResult(d))
	if d.DetectedMime != "application/pdf" || d.SHA256 != "aa" || d.ValidatedAt == nil {
		t.Errorf("document = %+v", d)
	}
}

func TestValidationResult_Validate(t *testing.T) {
	ok := ValidationResult{Outcome: OutcomePass, WorkerVersion: "v"}
	must(t, ok.Validate())
	for _, r := range []ValidationResult{
		{Outcome: "MAYBE", WorkerVersion: "v"},
		{Outcome: OutcomeFail, WorkerVersion: "v"},
		{Outcome: OutcomePass, FailureCodes: []string{"X"}, WorkerVersion: "v"},
		{Outcome: OutcomePass},
	} {
		wantCode(t, r.Validate(), CodeValidationFailed)
	}
}

func TestActorFor(t *testing.T) {
	if a := ActorFor("s1", []string{"citizen"}); a.Type != ActorCitizen {
		t.Errorf("citizen role -> %s", a.Type)
	}
	if a := ActorFor("s2", []string{"citizen", "caseworker"}); a.Type != ActorCaseworker {
		t.Errorf("caseworker role -> %s", a.Type)
	}
}

func TestFormatReference(t *testing.T) {
	if got := FormatReference(2026, 42); got != "CASE-2026-000042" {
		t.Errorf("got %q", got)
	}
}
