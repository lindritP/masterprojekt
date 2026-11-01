package events

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
)

// TestPayloadsMatchSchemas validates the payload of every event type against
// its committed schema in api/events/ (FR-020).
func TestPayloadsMatchSchemas(t *testing.T) {
	const id, at = "01a1976c-de50-790a-bbd3-1218e2a86582", "2026-11-01T14:04:17.614Z"
	sha := "5a0e42ca900d06f7cae577d9eeb7fab45653547db646a37961316bd15b418e85"
	payloads := map[string]any{
		"document.uploaded.v1": DocumentUploaded{
			CaseID: id, DocumentID: id, StorageKey: "cases/x/documents/y/original", Filename: "plan.pdf",
			DeclaredMime: "application/pdf", SizeBytes: 1000, UploadedAt: at,
		},
		"document.validated.v1": DocumentValidated{
			CaseID: id, DocumentID: id, Outcome: "FAIL", FailureCodes: []string{"CORRUPT_DOCUMENT"},
			Checks:        domain.Checks{DetectedMime: "application/pdf", SizeBytes: 1000, SHA256: sha},
			WorkerVersion: "c0465ff", DurationMs: 12,
		},
		"case.submitted.v1": CaseSubmitted{
			CaseID: id, Reference: "CASE-2026-000042", CaseType: "BENEFIT_CLAIM", SubjectID: "alice",
			DocumentIDs: []string{id}, SubmittedAt: at, Resubmission: false,
		},
		"case.decided.v1": CaseDecided{
			CaseID: id, Reference: "CASE-2026-000042", Decision: "REJECTED", Reason: "unsigned", DecidedBy: "carol", DecidedAt: at,
		},
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			schema := compile(t, filepath.Join("..", "..", "..", "..", "api", "events", name+".schema.json"))
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(inst); err != nil {
				t.Errorf("payload does not match its schema: %v", err)
			}
		})
	}
}

func compile(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource(path, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
