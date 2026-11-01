package pgrepo

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports/conformance"
)

// TestConformance runs the CaseRepository suite against the local PostgreSQL,
// connected as the application role (FR-030).
func TestConformance(t *testing.T) {
	conformance.RequireEnabled(t)
	port, _ := strconv.Atoi(env("CCSRA_DB_PORT", "5432"))
	repo, err := New(context.Background(), Config{
		Host:     env("CCSRA_DB_HOST", "localhost"),
		Port:     port,
		Name:     env("CCSRA_DB_NAME", "ccsra"),
		User:     env("CCSRA_DB_USER", "ccsra_app"),
		Password: env("CCSRA_DB_PASSWORD", "ccsra-app-local"),
		SSLMode:  env("CCSRA_DB_SSLMODE", "disable"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)

	conformance.TestCaseRepository(t, conformance.CaseRepositoryFixture{
		Repo: repo,
		TryRewriteAudit: func(ctx context.Context, caseID uuid.UUID) (error, error) {
			_, updateErr := repo.pool.Exec(ctx, `UPDATE audit_events SET event_type = 'tampered' WHERE case_id = $1`, caseID)
			_, deleteErr := repo.pool.Exec(ctx, `DELETE FROM audit_events WHERE case_id = $1`, caseID)
			return updateErr, deleteErr
		},
	})
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
