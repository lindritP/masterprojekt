package domain

import (
	"testing"

	"github.com/google/uuid"
)

// One test per invariant of SRS §3.3, each attempting a violation (FR-041).
// INV-3's same-transaction half and INV-6's stale-write half are repository
// properties; the port conformance suite covers them against PostgreSQL.

func TestINV1_TerminalCasesNeverTransition(t *testing.T) {
	for _, s := range []State{StateAccepted, StateRejected} {
		c := caseIn(t, s)
		_, err := c.Submit(opBy(alice))
		wantCode(t, err, CodeInvalidStateTransition)
		_, err = c.Decide(DecisionAccepted, "", opBy(carol))
		wantCode(t, err, CodeInvalidStateTransition)
		_, err = c.Decide(DecisionRejected, "again", opBy(carol))
		wantCode(t, err, CodeInvalidStateTransition)
		_, _, err = c.AttachDocument(uuid.New(), "late.pdf", "application/pdf", 10, policy, opBy(alice))
		wantCode(t, err, CodeDocumentsImmutable)
		if c.State != s {
			t.Errorf("terminal state %s became %s", s, c.State)
		}
	}
}

func TestINV2_DocumentsChangeOnlyInDraftOrNeedsInfo(t *testing.T) {
	for _, s := range []State{StateValidating, StateUnderReview, StateAccepted, StateRejected} {
		c := caseIn(t, s)
		d := c.Documents[0]
		_, _, err := c.AttachDocument(uuid.New(), "extra.pdf", "application/pdf", 10, policy, opBy(alice))
		wantCode(t, err, CodeDocumentsImmutable)
		_, err = c.RemoveDocument(d.ID, opBy(alice))
		wantCode(t, err, CodeDocumentsImmutable)
	}

	// An upload registered in DRAFT cannot be completed after submission.
	c := newDraft(t)
	uploaded(t, c, "a.pdf")
	pending, _, err := c.AttachDocument(uuid.New(), "b.pdf", "application/pdf", 10, policy, opBy(alice))
	must(t, err)
	_, err = c.Submit(opBy(alice))
	must(t, err)
	_, err = c.CompleteUpload(pending.ID, 10, policy.MaxBytes, opBy(alice))
	wantCode(t, err, CodeDocumentsImmutable)
}

func TestINV3_EveryStateChangeWritesExactlyOneEntry(t *testing.T) {
	c := newDraft(t)
	d := uploaded(t, c, "a.pdf")
	var all []AuditEntry
	collect := func(audit []AuditEntry, err error) {
		t.Helper()
		must(t, err)
		all = append(all, audit...)
	}

	collect(c.Submit(opBy(alice)))
	collect(record(t, c, failResult(d, "CORRUPT_DOCUMENT")), nil) // T5
	uploaded(t, c, "a.pdf")
	collect(c.Submit(opBy(alice)))                         // T3
	collect(record(t, c, passResult(c.Documents[1])), nil) // T4: the INVALID original is superseded
	collect(c.Decide(DecisionAccepted, "", opBy(carol)))   // T6

	var transitions []string
	for _, a := range all {
		if a.EventType == EventCaseStateChanged {
			transitions = append(transitions, a.Payload["transition"].(string))
		}
		if a.CaseID != c.ID || a.CorrelationID == "" || a.ActorID == "" || a.OccurredAt.IsZero() {
			t.Errorf("incomplete audit entry %+v", a)
		}
	}
	want := []string{"T2", "T5", "T3", "T4", "T6"}
	if len(transitions) != len(want) {
		t.Fatalf("transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Fatalf("transitions = %v, want %v", transitions, want)
		}
	}
}

func TestINV4_StorageKeyNeverChanges(t *testing.T) {
	c := caseIn(t, StateNeedsInfo)
	keys := map[uuid.UUID]string{}
	for _, d := range c.Documents {
		keys[d.ID] = d.StorageKey
		if d.StorageKey != StorageKey(c.ID, d.ID) {
			t.Fatalf("key %q does not follow the §5.8 layout", d.StorageKey)
		}
	}
	_, err := c.Submit(opBy(alice))
	must(t, err)
	record(t, c, passResult(c.Documents[len(c.Documents)-1]))
	_, err = c.Decide(DecisionRejected, "no", opBy(carol))
	must(t, err)
	for _, d := range c.Documents {
		if keys[d.ID] != d.StorageKey {
			t.Errorf("storage key of %s changed from %q to %q", d.ID, keys[d.ID], d.StorageKey)
		}
	}
}

func TestINV5_SubjectIDNeverChanges(t *testing.T) {
	c := caseIn(t, StateUnderReview)
	_, err := c.Submit(opBy(bob))
	wantCode(t, err, CodeForbiddenNotOwner)
	_, err = c.Decide(DecisionAccepted, "", opBy(carol))
	must(t, err)
	if c.SubjectID != "alice" {
		t.Errorf("subject changed to %q", c.SubjectID)
	}
}

func TestINV6_EveryMutationIncrementsVersionOnce(t *testing.T) {
	c := newDraft(t)
	steps := []func() error{
		func() error {
			_, _, err := c.AttachDocument(uuid.New(), "a.pdf", "application/pdf", 10, policy, opBy(alice))
			return err
		},
		func() error {
			_, err := c.CompleteUpload(c.Documents[0].ID, 10, policy.MaxBytes, opBy(alice))
			return err
		},
		func() error { _, err := c.Submit(opBy(alice)); return err },
		func() error { _, _, err := c.RecordValidation(passResult(c.Documents[0]), opBy(worker)); return err },
		func() error { _, err := c.Decide(DecisionAccepted, "", opBy(carol)); return err },
	}
	for i, step := range steps {
		before := c.Version
		must(t, step())
		if c.Version != before+1 {
			t.Errorf("step %d: version %d -> %d, want +1", i, before, c.Version)
		}
	}

	wantCode(t, c.ExpectVersion(c.Version-1), CodeConcurrentModification)
	must(t, c.ExpectVersion(c.Version))

	// A failed mutation leaves the version alone.
	before := c.Version
	_, err := c.Submit(opBy(alice))
	wantCode(t, err, CodeInvalidStateTransition)
	if c.Version != before {
		t.Errorf("failed mutation moved version %d -> %d", before, c.Version)
	}
}

func TestINV7_OneActiveDocumentPerFilename(t *testing.T) {
	// In DRAFT a duplicate filename is refused.
	c := newDraft(t)
	uploaded(t, c, "plan.pdf")
	_, _, err := c.AttachDocument(uuid.New(), "plan.pdf", "application/pdf", 10, policy, opBy(alice))
	wantCode(t, err, CodeValidationFailed)

	// In NEEDS_INFO it replaces the active one (FR-211).
	c = caseIn(t, StateNeedsInfo)
	active := 0
	for _, d := range c.Documents {
		if d.Filename == "plan.pdf" && d.active() {
			active++
		}
	}
	old, replacement := c.Documents[0], c.Documents[1]
	if active != 1 || old.Status != DocSuperseded || replacement.SupersedesID == nil || *replacement.SupersedesID != old.ID {
		t.Errorf("active=%d old=%s supersedes=%v", active, old.Status, replacement.SupersedesID)
	}
}

func TestINV8_ValidationResultIsWrittenOnce(t *testing.T) {
	c := caseIn(t, StateValidating)
	d := c.Documents[0]
	first := passResult(d)
	record(t, c, first)
	version := c.Version

	audit, replayed, err := c.RecordValidation(first, opBy(worker))
	must(t, err)
	if !replayed || audit != nil || c.Version != version {
		t.Errorf("identical repeat: replayed=%v audit=%v version %d->%d", replayed, audit, version, c.Version)
	}

	different := first
	different.DurationMs++
	_, _, err = c.RecordValidation(different, opBy(worker))
	wantCode(t, err, CodeDocumentAlreadyValidated)
}

func TestINV9_OwnershipAndRoles(t *testing.T) {
	c := caseIn(t, StateDraft)
	d := c.Documents[0]

	// Reads: a foreign citizen does not even learn the case exists.
	wantCode(t, c.AuthorizeRead(bob), CodeCaseNotFound)
	must(t, c.AuthorizeRead(alice))
	must(t, c.AuthorizeRead(carol))

	// Mutations by a foreign citizen, or by a caseworker acting as owner.
	for _, a := range []Actor{bob, carol} {
		_, _, err := c.AttachDocument(uuid.New(), "x.pdf", "application/pdf", 10, policy, opBy(a))
		wantCode(t, err, CodeForbiddenNotOwner)
		_, err = c.CompleteUpload(d.ID, 10, policy.MaxBytes, opBy(a))
		wantCode(t, err, CodeForbiddenNotOwner)
		_, err = c.RemoveDocument(d.ID, opBy(a))
		wantCode(t, err, CodeForbiddenNotOwner)
		_, err = c.Submit(opBy(a))
		wantCode(t, err, CodeForbiddenNotOwner)
	}

	// Only caseworkers decide, and only cases under review.
	_, err := c.Decide(DecisionAccepted, "", opBy(alice))
	wantCode(t, err, CodeForbiddenRole)
	_, err = c.Decide(DecisionAccepted, "", opBy(carol))
	wantCode(t, err, CodeInvalidStateTransition)

	// A subject holding both roles may file a case but never decide it.
	both := Actor{Type: ActorCaseworker, ID: "dana"}
	own, _, err := NewCase(uuid.New(), "CASE-2026-000099", string(CaseBenefitClaim), "Own claim", opBy(both))
	must(t, err)
	doc, _, err := own.AttachDocument(uuid.New(), "own.pdf", "application/pdf", 10, policy, opBy(both))
	must(t, err)
	_, err = own.CompleteUpload(doc.ID, 10, policy.MaxBytes, opBy(both))
	must(t, err)
	_, err = own.Submit(opBy(both))
	must(t, err)
	record(t, own, passResult(doc))
	_, err = own.Decide(DecisionAccepted, "", opBy(both))
	wantCode(t, err, CodeForbiddenRole)
	if own.State != StateUnderReview {
		t.Errorf("self-decision moved the case to %s", own.State)
	}

	// Only the worker reports validation results.
	_, _, err = c.RecordValidation(passResult(d), opBy(alice))
	wantCode(t, err, CodeForbiddenRole)
}
