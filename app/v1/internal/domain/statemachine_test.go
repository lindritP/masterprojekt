package domain

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// TestStateMachine_EveryStateTriggerPair covers FR-040: every transition in
// SRS §3.2, and a rejection for every (state, trigger) pair not in the table.
func TestStateMachine_EveryStateTriggerPair(t *testing.T) {
	// The target each allowed pair reaches when fire() satisfies its guard.
	want := map[State]map[Trigger]State{
		StateDraft:       {TriggerSubmit: StateValidating},
		StateNeedsInfo:   {TriggerSubmit: StateValidating},
		StateValidating:  {TriggerValidationComplete: StateUnderReview},
		StateUnderReview: {TriggerAccept: StateAccepted, TriggerReject: StateRejected},
	}

	for _, s := range States {
		for _, tr := range Triggers {
			t.Run(fmt.Sprintf("%s/%s", s, tr), func(t *testing.T) {
				c := caseIn(t, s)
				version := c.Version
				audit, err := fire(t, c, tr)

				to, ok := want[s][tr]
				if ok != Allowed(s, tr) {
					t.Fatalf("Allowed(%s, %s) = %v, test table says %v", s, tr, Allowed(s, tr), ok)
				}
				if ok {
					must(t, err)
					if c.State != to {
						t.Errorf("state = %s, want %s", c.State, to)
					}
					if stateChanges(audit) < 1 {
						t.Error("no state-change audit entry")
					}
					return
				}

				// Disallowed: the case is untouched. Recording a result is not a
				// command a caller issues, so outside VALIDATING it simply never
				// transitions; every other trigger is an explicit rejection.
				if tr != TriggerValidationComplete {
					wantCode(t, err, CodeInvalidStateTransition)
					if c.Version != version {
						t.Errorf("version moved from %d to %d on a rejected transition", version, c.Version)
					}
				}
				if c.State != s {
					t.Errorf("state = %s, want unchanged %s", c.State, s)
				}
				if stateChanges(audit) != 0 {
					t.Errorf("rejected transition wrote %d state-change entries", stateChanges(audit))
				}
			})
		}
	}
}

// fire issues trigger tr against c with its guard satisfied where possible.
func fire(t *testing.T, c *Case, tr Trigger) ([]AuditEntry, error) {
	t.Helper()
	switch tr {
	case TriggerSubmit:
		return c.Submit(opBy(alice))
	case TriggerAccept:
		return c.Decide(DecisionAccepted, "", opBy(carol))
	case TriggerReject:
		return c.Decide(DecisionRejected, "reason", opBy(carol))
	case TriggerValidationComplete:
		// Report a PASS for an uploaded document that has no result yet; if
		// none exists, replay an existing result (which must change nothing).
		for _, d := range c.Documents {
			if d.Result == nil && (d.Status == DocUploaded || d.Status == DocValidating) {
				audit, _, err := c.RecordValidation(passResult(d), opBy(worker))
				return audit, err
			}
		}
		for _, d := range c.Documents {
			if d.Result != nil {
				audit, _, err := c.RecordValidation(*d.Result, opBy(worker))
				return audit, err
			}
		}
	}
	t.Fatalf("cannot fire %s", tr)
	return nil, nil
}

func TestT1_CreateStartsInDraft(t *testing.T) {
	c, audit, err := NewCase(uuid.New(), "CASE-2026-000007", string(CaseBenefitClaim), "Child benefit", opBy(alice))
	must(t, err)
	if c.State != StateDraft || c.Version != 1 || c.SubjectID != "alice" {
		t.Errorf("got state %s, version %d, subject %q", c.State, c.Version, c.SubjectID)
	}
	if len(audit) != 1 || audit[0].EventType != EventCaseCreated {
		t.Errorf("audit = %+v, want one case.created entry", audit)
	}
}

func TestT1_RejectsUnknownCaseTypeAndBadTitle(t *testing.T) {
	_, _, err := NewCase(uuid.New(), "r", "PARKING_PERMIT", "x", opBy(alice))
	wantCode(t, err, CodeInvalidCaseType)

	long := make([]rune, maxTitleLength+1)
	for i := range long {
		long[i] = 'ä'
	}
	for _, title := range []string{"", "   ", string(long)} {
		_, _, err := NewCase(uuid.New(), "r", string(CaseBenefitClaim), title, opBy(alice))
		wantCode(t, err, CodeValidationFailed)
	}
	_, _, err = NewCase(uuid.New(), "r", string(CaseBenefitClaim), "ok", opBy(worker))
	wantCode(t, err, CodeForbiddenRole)
}

func TestT2_SubmitNeedsAnUploadedDocument(t *testing.T) {
	c := newDraft(t)
	_, err := c.Submit(opBy(alice))
	wantCode(t, err, CodeNoDocumentsAttached)

	// A registered but never completed upload does not count either.
	_, _, err = c.AttachDocument(uuid.New(), "a.pdf", "application/pdf", 10, policy, opBy(alice))
	must(t, err)
	_, err = c.Submit(opBy(alice))
	wantCode(t, err, CodeNoDocumentsAttached)

	d := uploaded(t, c, "b.pdf")
	audit, err := c.Submit(opBy(alice))
	must(t, err)
	if c.State != StateValidating || d.Status != DocValidating || c.SubmittedAt == nil {
		t.Errorf("state %s, document %s, submittedAt %v", c.State, d.Status, c.SubmittedAt)
	}
	if stateChanges(audit) != 1 || audit[0].Payload["transition"] != "T2" || audit[0].Payload["resubmission"] != false {
		t.Errorf("audit = %+v", audit)
	}
}

func TestT3_ResubmitAfterNeedsInfo(t *testing.T) {
	c := caseIn(t, StateNeedsInfo)
	firstSubmit := *c.SubmittedAt
	audit, err := c.Submit(opBy(alice))
	must(t, err)
	if c.State != StateValidating {
		t.Fatalf("state = %s", c.State)
	}
	if audit[0].Payload["transition"] != "T3" || audit[0].Payload["resubmission"] != true {
		t.Errorf("payload = %+v", audit[0].Payload)
	}
	if !c.SubmittedAt.Equal(firstSubmit) {
		t.Error("submittedAt must keep the first transition into VALIDATING")
	}
}

func TestT4_AllPassLeadsToUnderReview(t *testing.T) {
	c := newDraft(t)
	d1, d2 := uploaded(t, c, "a.pdf"), uploaded(t, c, "b.pdf")
	_, err := c.Submit(opBy(alice))
	must(t, err)

	if audit := record(t, c, passResult(d1)); c.State != StateValidating || stateChanges(audit) != 0 {
		t.Fatalf("after first result: state %s, %d state changes", c.State, stateChanges(audit))
	}
	audit := record(t, c, passResult(d2))
	if c.State != StateUnderReview || d1.Status != DocValid || d2.Status != DocValid {
		t.Errorf("state %s, documents %s/%s", c.State, d1.Status, d2.Status)
	}
	if stateChanges(audit) != 1 || audit[len(audit)-1].Payload["transition"] != "T4" {
		t.Errorf("audit = %+v", audit)
	}
}

func TestT5_AnyFailLeadsToNeedsInfo(t *testing.T) {
	c := newDraft(t)
	d1, d2 := uploaded(t, c, "a.pdf"), uploaded(t, c, "b.pdf")
	_, err := c.Submit(opBy(alice))
	must(t, err)
	record(t, c, failResult(d1, "ENCRYPTED_DOCUMENT"))
	audit := record(t, c, passResult(d2))
	if c.State != StateNeedsInfo || d1.Status != DocInvalid || d2.Status != DocValid {
		t.Errorf("state %s, documents %s/%s", c.State, d1.Status, d2.Status)
	}
	if audit[len(audit)-1].Payload["transition"] != "T5" {
		t.Errorf("audit = %+v", audit)
	}
}

// Results usually arrive before the citizen submits, because the worker starts
// on document.uploaded. Submitting then completes T2 and T4 in one operation.
func TestSubmit_WithResultsAlreadyIn_ChainsT2AndT4(t *testing.T) {
	c := newDraft(t)
	d := uploaded(t, c, "a.pdf")
	record(t, c, passResult(d))
	if c.State != StateDraft || d.Status != DocUploaded {
		t.Fatalf("a result before submit must not move the case: %s / %s", c.State, d.Status)
	}
	version := c.Version
	audit, err := c.Submit(opBy(alice))
	must(t, err)
	if c.State != StateUnderReview || d.Status != DocValid {
		t.Errorf("state %s, document %s", c.State, d.Status)
	}
	if stateChanges(audit) != 2 || audit[0].Payload["transition"] != "T2" || audit[1].Payload["transition"] != "T4" {
		t.Errorf("audit = %+v", audit)
	}
	if c.Version != version+1 {
		t.Errorf("one operation must increment the version once, got %d -> %d", version, c.Version)
	}
}

func TestT6_Accept(t *testing.T) {
	c := caseIn(t, StateUnderReview)
	_, err := c.Decide(DecisionAccepted, "", opBy(carol))
	must(t, err)
	if c.State != StateAccepted || c.DecidedBy != "carol" || c.DecidedAt == nil {
		t.Errorf("state %s, decidedBy %q, decidedAt %v", c.State, c.DecidedBy, c.DecidedAt)
	}
}

func TestT7_RejectNeedsReason(t *testing.T) {
	c := caseIn(t, StateUnderReview)
	_, err := c.Decide(DecisionRejected, "  ", opBy(carol))
	wantCode(t, err, CodeDecisionReasonRequired)
	if c.State != StateUnderReview {
		t.Fatalf("state moved to %s", c.State)
	}
	_, err = c.Decide(DecisionRejected, "missing signature", opBy(carol))
	must(t, err)
	if c.State != StateRejected || c.DecisionReason != "missing signature" {
		t.Errorf("state %s, reason %q", c.State, c.DecisionReason)
	}
}

func TestDecide_UnknownDecision(t *testing.T) {
	c := caseIn(t, StateUnderReview)
	_, err := c.Decide("MAYBE", "", opBy(carol))
	wantCode(t, err, CodeValidationFailed)
}
