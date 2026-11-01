package domain

import "slices"

// ActorType says in which capacity an actor acts (SRS §3.1, AuditEvent).
type ActorType string

const (
	ActorCitizen    ActorType = "CITIZEN"
	ActorCaseworker ActorType = "CASEWORKER"
	ActorSystem     ActorType = "SYSTEM"
)

// Roles carried by an authenticated subject (SRS §2.2).
const (
	RoleCitizen    = "citizen"
	RoleCaseworker = "caseworker"
)

// Actor performs an operation: a subject identified by its `sub` claim, or a
// service identified by its name.
type Actor struct {
	Type ActorType
	ID   string
}

// ActorFor maps an authenticated subject to the capacity it acts in.
// Holding the caseworker role makes the subject a caseworker; ownership checks
// still compare the subject id, so a caseworker can never act on someone
// else's case as its owner.
func ActorFor(subjectID string, roles []string) Actor {
	if slices.Contains(roles, RoleCaseworker) {
		return Actor{Type: ActorCaseworker, ID: subjectID}
	}
	return Actor{Type: ActorCitizen, ID: subjectID}
}

// System is the actor for an internal service, e.g. validation-worker.
func System(service string) Actor { return Actor{Type: ActorSystem, ID: service} }

// AuthorizeRead enforces INV-9 for reads. A citizen asking for another
// subject's case gets CASE_NOT_FOUND, so the case's existence is not
// disclosed (SRS §5.2).
func (c *Case) AuthorizeRead(a Actor) error {
	switch {
	case a.Type == ActorCaseworker || a.Type == ActorSystem:
		return nil
	case a.ID == c.SubjectID:
		return nil
	default:
		return fail(CodeCaseNotFound, "case %s not found", c.ID)
	}
}

// AuthorizeMutation enforces INV-9 for an owner-only operation that does not
// go through an aggregate method, e.g. issuing an upload URL.
func (c *Case) AuthorizeMutation(a Actor) error { return c.authorizeOwner(a) }

// authorizeOwner enforces INV-9 for citizen mutations.
func (c *Case) authorizeOwner(a Actor) error {
	if a.Type == ActorSystem || a.ID != c.SubjectID {
		return fail(CodeForbiddenNotOwner, "case %s belongs to another subject", c.ID)
	}
	return nil
}

func requireType(a Actor, t ActorType) error {
	if a.Type != t {
		return fail(CodeForbiddenRole, "operation requires a %s actor", t)
	}
	return nil
}
