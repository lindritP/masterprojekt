package domain

// State is a case state (SRS §3.2).
type State string

const (
	StateDraft       State = "DRAFT"
	StateValidating  State = "VALIDATING"
	StateNeedsInfo   State = "NEEDS_INFO"
	StateUnderReview State = "UNDER_REVIEW"
	StateAccepted    State = "ACCEPTED"
	StateRejected    State = "REJECTED"
)

// States lists every case state.
var States = []State{StateDraft, StateValidating, StateNeedsInfo, StateUnderReview, StateAccepted, StateRejected}

// Terminal reports whether no transition may leave s (INV-1).
func (s State) Terminal() bool { return s == StateAccepted || s == StateRejected }

// Trigger is what drives a transition.
type Trigger string

const (
	TriggerSubmit             Trigger = "submit"
	TriggerValidationComplete Trigger = "validation-complete" // last outstanding result recorded
	TriggerAccept             Trigger = "accept"
	TriggerReject             Trigger = "reject"
)

// Triggers lists every trigger.
var Triggers = []Trigger{TriggerSubmit, TriggerValidationComplete, TriggerAccept, TriggerReject}

// Transition is one row of the SRS §3.2 table. T1 (creation) is NewCase.
type Transition struct {
	ID      string
	From    State
	Trigger Trigger
	To      State
}

// Transitions is the complete table. Any (state, trigger) pair not listed is
// rejected with INVALID_STATE_TRANSITION.
var Transitions = []Transition{
	{"T2", StateDraft, TriggerSubmit, StateValidating},
	{"T3", StateNeedsInfo, TriggerSubmit, StateValidating},
	{"T4", StateValidating, TriggerValidationComplete, StateUnderReview},
	{"T5", StateValidating, TriggerValidationComplete, StateNeedsInfo},
	{"T6", StateUnderReview, TriggerAccept, StateAccepted},
	{"T7", StateUnderReview, TriggerReject, StateRejected},
}

// Allowed reports whether trigger t may fire in state s at all.
func Allowed(s State, t Trigger) bool {
	for _, tr := range Transitions {
		if tr.From == s && tr.Trigger == t {
			return true
		}
	}
	return false
}

func lookup(from State, t Trigger, to State) (Transition, bool) {
	for _, tr := range Transitions {
		if tr.From == from && tr.Trigger == t && tr.To == to {
			return tr, true
		}
	}
	return Transition{}, false
}

func checkAllowed(s State, t Trigger) error {
	if !Allowed(s, t) {
		return fail(CodeInvalidStateTransition, "case is in state %s; %s is not permitted", s, t)
	}
	return nil
}
