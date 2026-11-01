package conformance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// CaseRepositoryFixture provides a live CaseRepository adapter.
type CaseRepositoryFixture struct {
	Repo ports.CaseRepository

	// TryRewriteAudit attempts an UPDATE and a DELETE of the case's audit
	// rows with the application's own credentials. Both must fail (FR-030).
	TryRewriteAudit func(ctx context.Context, caseID uuid.UUID) (updateErr, deleteErr error)
}

var docPolicy = domain.DocumentPolicy{MaxDocuments: 10, MaxBytes: 1 << 20, AllowedMime: []string{pdfType}}

// TestCaseRepository runs the CaseRepository semantics of SRS §7.4.
func TestCaseRepository(t *testing.T, f CaseRepositoryFixture) {
	ctx := context.Background()
	repo := f.Repo
	owner := domain.Actor{Type: domain.ActorCitizen, ID: "conformance-" + RunID()}
	worker := domain.System("validation-worker")
	op := func(a domain.Actor) domain.Op {
		return domain.Op{Actor: a, CorrelationID: "conformance", At: time.Now().UTC().Truncate(time.Microsecond)}
	}

	newCase := func(t *testing.T) *domain.Case {
		t.Helper()
		ref, err := repo.AllocateReference(ctx, time.Now().Year())
		mustOK(t, err)
		c, audit, err := domain.NewCase(uuid.Must(uuid.NewV7()), ref, string(domain.CaseBenefitClaim), "Conformance case", op(owner))
		mustOK(t, err)
		mustOK(t, repo.Create(ctx, c, audit))
		return c
	}
	attach := func(name string, upload bool) func(*domain.Case) ([]domain.AuditEntry, error) {
		return func(c *domain.Case) ([]domain.AuditEntry, error) {
			d, audit, err := c.AttachDocument(uuid.Must(uuid.NewV7()), name, pdfType, 100, docPolicy, op(owner))
			if err != nil || !upload {
				return audit, err
			}
			more, err := c.CompleteUpload(d.ID, 100, docPolicy.MaxBytes, op(owner))
			return append(audit, more...), err
		}
	}
	auditCount := func(t *testing.T, id uuid.UUID) int {
		t.Helper()
		entries, _, err := repo.ListAudit(ctx, id, 100, "")
		mustOK(t, err)
		return len(entries)
	}

	t.Run("Create_GetRoundTrip", func(t *testing.T) {
		c := newCase(t)
		got, err := repo.Get(ctx, c.ID)
		mustOK(t, err)
		if got.Reference != c.Reference || got.State != domain.StateDraft || got.SubjectID != owner.ID || got.Version != 1 || !got.CreatedAt.Equal(c.CreatedAt) {
			t.Errorf("got %+v, want the created case", got)
		}
		if _, err := repo.Get(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("Get of an unknown id: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Transition_IsAtomicWithAudit", func(t *testing.T) {
		c := newCase(t)
		before := auditCount(t, c.ID)

		boom := errors.New("handler failed after mutating")
		err := repo.Transition(ctx, c.ID, c.Version, func(c *domain.Case) ([]domain.AuditEntry, error) {
			if _, err := attach("a.pdf", true)(c); err != nil {
				return nil, err
			}
			return nil, boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Transition err = %v, want the handler's error", err)
		}
		got, err := repo.Get(ctx, c.ID)
		mustOK(t, err)
		if got.Version != c.Version || len(got.Documents) != 0 || auditCount(t, c.ID) != before {
			t.Errorf("failed transition left traces: version %d, %d documents, %d audit entries (want %d, 0, %d)",
				got.Version, len(got.Documents), auditCount(t, c.ID), c.Version, before)
		}

		// Attach and complete are two domain mutations: two versions, two entries.
		mustOK(t, repo.Transition(ctx, c.ID, c.Version, attach("a.pdf", true)))
		got, err = repo.Get(ctx, c.ID)
		mustOK(t, err)
		if got.Version != c.Version+2 || len(got.Documents) != 1 || auditCount(t, c.ID) != before+2 {
			t.Errorf("successful transition: version %d, %d documents, %d audit entries", got.Version, len(got.Documents), auditCount(t, c.ID))
		}
	})

	t.Run("Transition_RejectsStaleVersion", func(t *testing.T) {
		c := newCase(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				errs[i] = repo.Transition(ctx, c.ID, c.Version, attach([]string{"x.pdf", "y.pdf"}[i], false))
			})
		}
		wg.Wait()
		ok, conflicts := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case domain.CodeOf(err) == domain.CodeConcurrentModification:
				conflicts++
			default:
				t.Errorf("unexpected error %v", err)
			}
		}
		if ok != 1 || conflicts != 1 {
			t.Errorf("%d succeeded and %d conflicted, want exactly 1 and 1", ok, conflicts)
		}
	})

	t.Run("RecordValidationResult_IsIdempotent", func(t *testing.T) {
		c := newCase(t)
		mustOK(t, repo.Transition(ctx, c.ID, 0, attach("doc.pdf", true)))
		loaded, err := repo.Get(ctx, c.ID)
		mustOK(t, err)
		d := loaded.Documents[0]
		res := domain.ValidationResult{
			DocumentID: d.ID, Outcome: domain.OutcomePass, WorkerVersion: "conformance", DurationMs: 5,
			Checks: domain.Checks{DetectedMime: pdfType, SizeBytes: 100, SHA256: strings.Repeat("ab", 32), PageCount: 1},
		}
		key := "msg-" + RunID()

		replayed, err := repo.RecordValidationResult(ctx, key, res, op(worker))
		mustOK(t, err)
		if replayed {
			t.Fatal("first write reported as replay")
		}
		after, err := repo.Get(ctx, c.ID)
		mustOK(t, err)

		replayed, err = repo.RecordValidationResult(ctx, key, res, op(worker))
		mustOK(t, err)
		again, err := repo.Get(ctx, c.ID)
		mustOK(t, err)
		if !replayed || again.Version != after.Version {
			t.Errorf("same key again: replayed=%v, version %d -> %d", replayed, after.Version, again.Version)
		}
		stored := again.Documents[0].Result
		if stored == nil || !stored.SameAs(res) {
			t.Fatalf("stored result %+v, want %+v", stored, res)
		}

		replayed, err = repo.RecordValidationResult(ctx, key+"-other", res, op(worker))
		mustOK(t, err)
		if !replayed {
			t.Error("identical result under a new key must be an idempotent replay (INV-8)")
		}
		different := res
		different.DurationMs++
		_, err = repo.RecordValidationResult(ctx, key+"-different", different, op(worker))
		if domain.CodeOf(err) != domain.CodeDocumentAlreadyValidated {
			t.Errorf("different second result: err = %v, want DOCUMENT_ALREADY_VALIDATED", err)
		}
	})

	t.Run("AuditLog_IsAppendOnly", func(t *testing.T) {
		c := newCase(t)
		updateErr, deleteErr := f.TryRewriteAudit(ctx, c.ID)
		if updateErr == nil || deleteErr == nil {
			t.Errorf("audit rows could be rewritten: update err = %v, delete err = %v", updateErr, deleteErr)
		}
		if auditCount(t, c.ID) != 1 {
			t.Errorf("audit trail changed: %d entries", auditCount(t, c.ID))
		}
	})

	t.Run("List_PaginatesByCursor", func(t *testing.T) {
		lister := domain.Actor{Type: domain.ActorCitizen, ID: "lister-" + RunID()}
		var ids []uuid.UUID
		for range 3 {
			ref, err := repo.AllocateReference(ctx, 2026)
			mustOK(t, err)
			c, audit, err := domain.NewCase(uuid.Must(uuid.NewV7()), ref, string(domain.CaseBuildingPermit), "List case", op(lister))
			mustOK(t, err)
			mustOK(t, repo.Create(ctx, c, audit))
			ids = append(ids, c.ID)
		}
		page1, next, err := repo.List(ctx, ports.ListFilter{SubjectID: lister.ID, Limit: 2})
		mustOK(t, err)
		page2, last, err := repo.List(ctx, ports.ListFilter{SubjectID: lister.ID, Limit: 2, After: next})
		mustOK(t, err)
		if len(page1) != 2 || next == "" || len(page2) != 1 || last != "" {
			t.Fatalf("pages of %d and %d (cursors %q, %q), want 2 then 1", len(page1), len(page2), next, last)
		}
		if page1[0].ID != ids[2] || page1[1].ID != ids[1] || page2[0].ID != ids[0] {
			t.Error("cases not listed newest first")
		}
	})
}
