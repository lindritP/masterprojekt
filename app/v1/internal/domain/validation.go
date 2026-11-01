package domain

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// Outcome is the overall result of validating one document.
type Outcome string

const (
	OutcomePass Outcome = "PASS"
	OutcomeFail Outcome = "FAIL"
)

// Checks are the observations behind an outcome (SRS §3.1).
type Checks struct {
	DetectedMime  string `json:"detectedMime"`
	SizeBytes     int64  `json:"sizeBytes"`
	SHA256        string `json:"sha256"`
	PageCount     int    `json:"pageCount"`
	ContentDigest string `json:"contentDigest"`
	ThumbnailKey  string `json:"thumbnailKey"`
}

// ValidationResult is written at most once per document (INV-8).
type ValidationResult struct {
	ID            uuid.UUID
	DocumentID    uuid.UUID
	Outcome       Outcome
	FailureCodes  []string
	Checks        Checks
	WorkerVersion string
	DurationMs    int
	CreatedAt     time.Time
}

// Validate checks the result is internally consistent: PASS exactly when
// there are no failure codes.
func (r ValidationResult) Validate() error {
	switch {
	case r.Outcome != OutcomePass && r.Outcome != OutcomeFail:
		return invalidField("outcome", "must be PASS or FAIL, got %q", r.Outcome)
	case r.Outcome == OutcomePass && len(r.FailureCodes) > 0:
		return invalidField("failureCodes", "must be empty on PASS")
	case r.Outcome == OutcomeFail && len(r.FailureCodes) == 0:
		return invalidField("failureCodes", "must not be empty on FAIL")
	case r.WorkerVersion == "":
		return invalidField("workerVersion", "must not be empty")
	}
	return nil
}

// SameAs reports whether two results carry identical content. The id and
// creation time are assigned on write and do not count (INV-8).
func (r ValidationResult) SameAs(o ValidationResult) bool {
	return r.DocumentID == o.DocumentID &&
		r.Outcome == o.Outcome &&
		slices.Equal(r.FailureCodes, o.FailureCodes) &&
		r.Checks == o.Checks &&
		r.WorkerVersion == o.WorkerVersion &&
		r.DurationMs == o.DurationMs
}
