// Package events defines the event contracts of SRS §5.6 (CloudEvents 1.0,
// partition key = caseId). Payload schemas are in api/events/.
package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Event types.
const (
	TypeDocumentUploaded  = "de.masterprojekt.ccsra.document.uploaded.v1"
	TypeDocumentValidated = "de.masterprojekt.ccsra.document.validated.v1"
	TypeCaseSubmitted     = "de.masterprojekt.ccsra.case.submitted.v1"
	TypeCaseDecided       = "de.masterprojekt.ccsra.case.decided.v1"
)

type DocumentUploaded struct {
	CaseID       string `json:"caseId"`
	DocumentID   string `json:"documentId"`
	StorageKey   string `json:"storageKey"`
	Filename     string `json:"filename"`
	DeclaredMime string `json:"declaredMime"`
	SizeBytes    int64  `json:"sizeBytes"`
	UploadedAt   string `json:"uploadedAt"`
}

type DocumentValidated struct {
	CaseID        string        `json:"caseId"`
	DocumentID    string        `json:"documentId"`
	Outcome       string        `json:"outcome"`
	FailureCodes  []string      `json:"failureCodes"`
	Checks        domain.Checks `json:"checks"`
	WorkerVersion string        `json:"workerVersion"`
	DurationMs    int           `json:"durationMs"`
}

type CaseSubmitted struct {
	CaseID       string   `json:"caseId"`
	Reference    string   `json:"reference"`
	CaseType     string   `json:"caseType"`
	SubjectID    string   `json:"subjectId"`
	DocumentIDs  []string `json:"documentIds"`
	SubmittedAt  string   `json:"submittedAt"`
	Resubmission bool     `json:"resubmission"`
}

type CaseDecided struct {
	CaseID    string `json:"caseId"`
	Reference string `json:"reference"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	DecidedBy string `json:"decidedBy"`
	DecidedAt string `json:"decidedAt"`
}

// Publish wraps data in an event of the given type and publishes it with the
// case id as partition key. The correlation id comes from ctx (FR-014).
func Publish(ctx context.Context, bus ports.EventBus, eventType, source, caseID string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return bus.Publish(ctx, ports.Event{
		Type:          eventType,
		Source:        source,
		ID:            uuid.Must(uuid.NewV7()).String(),
		Time:          time.Now().UTC(),
		Subject:       caseID,
		CorrelationID: platform.CorrelationID(ctx),
		Data:          raw,
	}, caseID)
}
