// Package worker is the message handler of validation-worker (SRS §4.5): it
// validates an uploaded document and reports the result. It reads and writes
// objects only through presigned URLs from intake-service (FR-004) and holds
// no database connection (FR-005).
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/domain"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/events"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/pipeline"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

const (
	source           = "/ccsra/validation-worker"
	thumbnailMaxSize = 1 << 20
)

// Worker handles document.uploaded deliveries.
type Worker struct {
	Validator        *pipeline.Validator
	Intake           *platform.Client // presigned URLs
	Cases            *platform.Client // validation results
	Bus              ports.EventBus
	HTTP             *http.Client // talks to the object store through presigned URLs
	MaxObjectBytes   int64
	MaxDeliveryCount int
	Log              *slog.Logger

	processed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
}

// RegisterMetrics adds the per-message metrics of FR-409 to reg.
func (w *Worker) RegisterMetrics(reg prometheus.Registerer) {
	w.processed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ccsra_messages_processed_total", Help: "Processed messages by settlement and validation outcome.",
	}, []string{"settlement", "outcome"})
	w.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "ccsra_message_processing_seconds", Help: "Message processing duration by validation outcome.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30},
	}, []string{"outcome"})
	reg.MustRegister(w.processed, w.duration)
}

// permanent marks a failure that redelivery cannot fix.
type permanent struct{ error }

// Handle processes one delivery and decides its settlement (FR-406).
func (w *Worker) Handle(ctx context.Context, d ports.Delivery) ports.Outcome {
	ctx = platform.WithCorrelationID(ctx, d.Event.CorrelationID)
	start := time.Now()
	outcome, err := w.process(ctx, d)

	settlement := ports.OutcomeAck
	label := "ack"
	switch {
	case err == nil:
	case errors.As(err, new(permanent)) || d.DeliveryCount >= w.MaxDeliveryCount:
		settlement, label = ports.OutcomeDeadLetter, "dead-letter"
		w.Log.ErrorContext(ctx, "message dead-lettered", "messageId", d.MessageID, "deliveryCount", d.DeliveryCount, "error", err)
	default:
		settlement, label = ports.OutcomeRetry, "retry"
		w.Log.WarnContext(ctx, "message will be retried", "messageId", d.MessageID, "deliveryCount", d.DeliveryCount, "error", err)
	}
	if outcome == "" {
		outcome = "none"
	}
	if w.processed != nil {
		w.processed.With(prometheus.Labels{"settlement": label, "outcome": outcome}).Inc()
		w.duration.With(prometheus.Labels{"outcome": outcome}).Observe(time.Since(start).Seconds())
	}
	return settlement
}

func (w *Worker) process(ctx context.Context, d ports.Delivery) (string, error) {
	if d.Event.Type != events.TypeDocumentUploaded {
		return "", nil // not ours; the queue binding should prevent this
	}
	var in events.DocumentUploaded
	if err := json.Unmarshal(d.Event.Data, &in); err != nil {
		return "", permanent{fmt.Errorf("undecodable event: %w", err)}
	}
	caseID, err1 := uuid.Parse(in.CaseID)
	docID, err2 := uuid.Parse(in.DocumentID)
	if err := errors.Join(err1, err2); err != nil {
		return "", permanent{fmt.Errorf("bad ids in event: %w", err)}
	}
	start := time.Now()

	// FR-401: fetch through a presigned GET.
	data, err := w.download(ctx, in.StorageKey)
	if err != nil {
		return "", err
	}

	// FR-402: steps 1–5.
	res, err := w.Validator.Validate(ctx, data, in.DeclaredMime)
	if err != nil {
		return "", err // infrastructure, not the document
	}
	checks := domain.Checks{
		DetectedMime: res.Checks.DetectedMime, SizeBytes: res.Checks.SizeBytes, SHA256: res.Checks.SHA256,
		PageCount: res.Checks.PageCount, ContentDigest: res.Checks.ContentDigest,
	}
	codes := res.FailureCodes

	// Step 6: thumbnail, only for a document that passed 1–5.
	if res.Passed() {
		key := domain.ThumbnailKey(caseID, docID)
		if err := w.thumbnail(ctx, data, key); err != nil {
			if platform.IsTransient(err) {
				return "", err
			}
			w.Log.WarnContext(ctx, "thumbnail failed", "documentId", docID, "error", err)
			codes = append(codes, pipeline.RenderFailed)
		} else {
			checks.ThumbnailKey = key
		}
	}

	outcome := domain.OutcomePass
	if len(codes) > 0 {
		outcome = domain.OutcomeFail
	}
	result := events.DocumentValidated{
		CaseID: in.CaseID, DocumentID: in.DocumentID, Outcome: string(outcome), FailureCodes: codes,
		Checks: checks, WorkerVersion: platform.Version, DurationMs: int(time.Since(start).Milliseconds()),
	}
	if result.FailureCodes == nil {
		result.FailureCodes = []string{}
	}

	// FR-403 / FR-404: report idempotently, then publish.
	header := http.Header{platform.HeaderIdempotencyKey: {d.MessageID}}
	_, err = w.Cases.Do(ctx, http.MethodPost, "/internal/v1/documents/"+docID.String()+"/validation-result", header, map[string]any{
		"outcome": result.Outcome, "failureCodes": result.FailureCodes, "checks": result.Checks,
		"workerVersion": result.WorkerVersion, "durationMs": result.DurationMs,
	}, nil)
	if p, ok := errors.AsType[*platform.Problem](err); ok && p.Code == domain.CodeDocumentAlreadyValidated {
		// A duplicate document.uploaded (e.g. a repeated completion) found the
		// document already validated: the work is done.
		w.Log.InfoContext(ctx, "document already validated", "documentId", docID)
		return string(outcome), nil
	}
	if err != nil {
		return "", classify(err)
	}
	if err := events.Publish(ctx, w.Bus, events.TypeDocumentValidated, source, in.CaseID, result); err != nil {
		w.Log.ErrorContext(ctx, "event lost after recording", "type", events.TypeDocumentValidated, "documentId", docID, "error", err)
	}
	return string(outcome), nil
}

// download fetches an object through a presigned GET, reading at most one
// byte more than the limit so that an oversized object is still recognised.
func (w *Worker) download(ctx context.Context, key string) ([]byte, error) {
	u, err := w.presign(ctx, map[string]any{"key": key, "operation": "GET", "ttlSeconds": 300})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", key, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, permanent{fmt.Errorf("download %s: object not found", key)}
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("download %s: status %d", key, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, w.MaxObjectBytes+1))
}

// thumbnail renders the step 6 PNG and stores it through a presigned upload.
func (w *Worker) thumbnail(ctx context.Context, data []byte, key string) error {
	png, err := w.Validator.Thumbnail(ctx, data)
	if err != nil {
		return err
	}
	u, err := w.presign(ctx, map[string]any{
		"key": key, "operation": "UPLOAD", "ttlSeconds": 300, "contentType": "image/png", "maxSizeBytes": thumbnailMaxSize,
	})
	if err != nil {
		return err
	}
	req, err := uploadRequest(ctx, u, "image/png", png)
	if err != nil {
		return err
	}
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return platform.NewProblem(platform.CodeUpstreamUnavailable, "thumbnail upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("thumbnail upload: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

type presigned struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	RequiredHeaders map[string]string `json:"requiredHeaders"`
	FormFields      map[string]string `json:"formFields"`
}

func (w *Worker) presign(ctx context.Context, body map[string]any) (presigned, error) {
	var u presigned
	if _, err := w.Intake.Do(ctx, http.MethodPost, "/internal/v1/objects/presign", nil, body, &u); err != nil {
		return presigned{}, classify(err)
	}
	return u, nil
}

// uploadRequest builds a PUT or POST-form upload for a presigned URL.
func uploadRequest(ctx context.Context, u presigned, contentType string, body []byte) (*http.Request, error) {
	if u.Method == http.MethodPut {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range u.RequiredHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", contentType)
		return req, nil
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range u.FormFields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	fw, err := mw.CreateFormFile("file", "thumbnail.png")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(body); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.URL, &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, nil
}

// classify turns a 4xx upstream answer into a permanent failure; anything
// else (5xx, unreachable) stays transient.
func classify(err error) error {
	if p, ok := errors.AsType[*platform.Problem](err); ok && p.Status >= 400 && p.Status < 500 {
		return permanent{err}
	}
	return err
}
