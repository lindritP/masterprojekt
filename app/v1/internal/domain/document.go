package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DocumentStatus is the lifecycle state of a document (SRS §3.1, FR-205).
type DocumentStatus string

const (
	DocPendingUpload DocumentStatus = "PENDING_UPLOAD"
	DocUploaded      DocumentStatus = "UPLOADED"
	DocValidating    DocumentStatus = "VALIDATING"
	DocValid         DocumentStatus = "VALID"
	DocInvalid       DocumentStatus = "INVALID"
	DocSuperseded    DocumentStatus = "SUPERSEDED"
	DocDeleted       DocumentStatus = "DELETED"
)

const maxFilenameLength = 255

// Document is a file attached to a case.
type Document struct {
	ID           uuid.UUID
	CaseID       uuid.UUID
	Filename     string
	DeclaredMime string
	DetectedMime string // set from the validation result
	SizeBytes    int64  // 0 until the upload is confirmed
	SHA256       string // set from the validation result
	StorageKey   string // immutable once assigned (INV-4)
	Status       DocumentStatus
	SupersedesID *uuid.UUID
	CreatedAt    time.Time
	UploadedAt   *time.Time
	ValidatedAt  *time.Time
	Result       *ValidationResult // nil until the worker reports
}

// StorageKey is the object key of a document's original (SRS §5.8).
func StorageKey(caseID, documentID uuid.UUID) string {
	return fmt.Sprintf("cases/%s/documents/%s/original", caseID, documentID)
}

// ThumbnailKey is the object key of a document's thumbnail (SRS §5.8).
func ThumbnailKey(caseID, documentID uuid.UUID) string {
	return fmt.Sprintf("cases/%s/documents/%s/thumbnail.png", caseID, documentID)
}

// active reports whether the document still counts towards the case: neither
// replaced nor removed (INV-7).
func (d *Document) active() bool { return d.Status != DocSuperseded && d.Status != DocDeleted }

// submitted reports whether the document is part of a submission.
func (d *Document) submitted() bool {
	return d.Status == DocValidating || d.Status == DocValid || d.Status == DocInvalid
}

// settle moves a submitted document to VALID or INVALID according to its result.
func (d *Document) settle() {
	if d.Result.Outcome == OutcomePass {
		d.Status = DocValid
	} else {
		d.Status = DocInvalid
	}
}
