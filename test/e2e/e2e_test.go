// Package e2e is the E2E conformance suite (SRS §10.2): the operational
// definition of "the application still works after the switch". It uses the
// public API only (FR-046), must pass unchanged against V0 and V1 on every
// target (FR-045), and creates its own cases on every run (FR-047).
//
// Acceptance criteria covered elsewhere: AC-07 (TTL expiry) by the port
// conformance test PresignUpload_Expires; AC-24 (dead-lettering) by
// DeadLetter_* and the worker's settlement logic; AC-26 by the corpus
// determinism test; AC-28 by AuditLog_IsAppendOnly; AC-29/AC-30 by the
// platform unit tests; AC-31 by ApplyRetention_*; AC-33 (SIGTERM) is checked
// manually; AC-34 needs V0 (stage B5).
package e2e

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAC01_CreateCase(t *testing.T) {
	r := call(t, citizen, "POST", "/api/v1/cases", map[string]string{"caseType": "BENEFIT_CLAIM", "title": "Child benefit"}, nil).
		expect(t, http.StatusCreated)
	var c caseDetail
	r.json(t, &c)
	if c.State != "DRAFT" || c.Version != 1 || !strings.HasPrefix(c.Reference, "CASE-") {
		t.Errorf("got %+v", c)
	}
	if r.Header.Get("ETag") != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", r.Header.Get("ETag"))
	}
	call(t, citizen, "POST", "/api/v1/cases", map[string]string{"caseType": "PARKING", "title": "x"}, nil).
		expectProblem(t, http.StatusBadRequest, "INVALID_CASE_TYPE")
}

func TestAC02_Unauthenticated(t *testing.T) {
	call(t, nil, "GET", "/api/v1/cases", nil, nil).expectProblem(t, http.StatusUnauthorized, "UNAUTHENTICATED")
	call(t, nil, "GET", "/api/v1/cases", nil, map[string]string{"Authorization": "Bearer not-a-token"}).
		expectProblem(t, http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestAC03_CitizenCannotUseWorkerEndpoints(t *testing.T) {
	call(t, citizen, "GET", "/api/v1/worker/cases", nil, nil).expectProblem(t, http.StatusForbidden, "FORBIDDEN_ROLE")
}

func TestAC04_ForeignCaseIsNotDisclosed(t *testing.T) {
	c := createCase(t, citizen)
	call(t, citizen2, "GET", "/api/v1/cases/"+c.ID, nil, nil).expectProblem(t, http.StatusNotFound, "CASE_NOT_FOUND")
	// Mutations by a foreign subject are refused as such.
	submit(t, citizen2, c).expectProblem(t, http.StatusForbidden, "FORBIDDEN_NOT_OWNER")
}

func TestAC05_RegisterIssuesPresignedUpload(t *testing.T) {
	c := createCase(t, citizen)
	u := register(t, citizen, c.ID, "plan.pdf", 1000)
	if u.UploadURL == "" || u.ExpiresAt == "" || (u.UploadMethod != "POST" && u.UploadMethod != "PUT") {
		t.Errorf("got %+v", u)
	}
	if d := getCase(t, citizen, c.ID).doc(u.DocumentID); d.Status != "PENDING_UPLOAD" {
		t.Errorf("document status %q, want PENDING_UPLOAD", d.Status)
	}
	call(t, citizen, "POST", "/api/v1/cases/"+c.ID+"/documents",
		map[string]any{"filename": "x.png", "declaredMime": "image/png", "sizeBytes": 10}, nil).
		expectProblem(t, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE")
}

func TestAC06_StorageEnforcesTheUploadConstraints(t *testing.T) {
	c := createCase(t, citizen)
	data := fixture(t, "valid-1page.pdf")
	u := register(t, citizen, c.ID, "plan.pdf", len(data))
	if s := put(t, u, "text/plain", data); s < 400 || s >= 500 {
		t.Errorf("wrong Content-Type: storage answered %d, want 4xx", s)
	}
	if s := put(t, u, "application/pdf", append(data, make([]byte, 10)...)); s < 400 || s >= 500 {
		t.Errorf("larger than declared: storage answered %d, want 4xx", s)
	}
}

func TestAC08_AC09_CompleteUpload(t *testing.T) {
	c := createCase(t, citizen)
	u := register(t, citizen, c.ID, "plan.pdf", 1000)
	complete := "/api/v1/cases/" + c.ID + "/documents/" + u.DocumentID + "/complete"
	call(t, citizen, "POST", complete, map[string]any{}, idempotent(nil)).
		expectProblem(t, http.StatusConflict, "UPLOAD_NOT_COMPLETED") // AC-09

	data := fixture(t, "valid-1page.pdf")
	u = register(t, citizen, c.ID, "valid.pdf", len(data))
	if s := put(t, u, "application/pdf", data); s >= 300 {
		t.Fatalf("upload: %d", s)
	}
	var out struct {
		Status    string `json:"status"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	call(t, citizen, "POST", "/api/v1/cases/"+c.ID+"/documents/"+u.DocumentID+"/complete", map[string]any{}, idempotent(nil)).
		expect(t, http.StatusOK).json(t, &out)
	if out.Status != "UPLOADED" || out.SizeBytes != int64(len(data)) {
		t.Errorf("got %+v", out)
	}
	// document.uploaded was published: the worker validates the document.
	waitFor(t, citizen, c.ID, "validation", validated(u.DocumentID))
}

func TestAC10_SubmitNeedsAnUploadedDocument(t *testing.T) {
	c := createCase(t, citizen)
	submit(t, citizen, c).expectProblem(t, http.StatusConflict, "NO_DOCUMENTS_ATTACHED")
}

// AC-11 and AC-12. With ADR 0012 the document is often validated before the
// submit, so the case may go straight on to UNDER_REVIEW.
func TestAC11_AC12_SubmitAndPass(t *testing.T) {
	c := createCase(t, citizen)
	doc := attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	c = getCase(t, citizen, c.ID)
	var out struct {
		State   string `json:"state"`
		Version int    `json:"version"`
	}
	submit(t, citizen, c).expect(t, http.StatusAccepted).json(t, &out)
	if out.State != "VALIDATING" && out.State != "UNDER_REVIEW" {
		t.Errorf("state after submit %q", out.State)
	}
	c = waitFor(t, citizen, c.ID, "UNDER_REVIEW", inState("UNDER_REVIEW"))
	d := c.doc(doc)
	if d.Status != "VALID" || d.Validation.Outcome != "PASS" || d.Validation.Checks.ContentDigest == "" || d.Validation.Checks.ThumbnailKey == "" {
		t.Errorf("document %+v", d)
	}
}

// AC-13, AC-14, AC-15, AC-23: a corrupt document, its replacement, resubmission.
func TestAC13_AC14_AC15_AC23_NeedsInfoLoop(t *testing.T) {
	c := createCase(t, citizen)
	bad := attach(t, citizen, c.ID, "plan.pdf", "corrupt.pdf")
	c = getCase(t, citizen, c.ID)
	submit(t, citizen, c).expect(t, http.StatusAccepted)
	c = waitFor(t, citizen, c.ID, "NEEDS_INFO", inState("NEEDS_INFO"))
	if d := c.doc(bad); d.Status != "INVALID" || len(d.Validation.FailureCodes) == 0 || d.Validation.FailureCodes[0] != "CORRUPT_DOCUMENT" {
		t.Fatalf("corrupt document %+v", d)
	}

	good := attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf") // same filename
	c = getCase(t, citizen, c.ID)
	if c.doc(bad).Status != "SUPERSEDED" || c.doc(good).SupersedesID == nil || *c.doc(good).SupersedesID != bad {
		t.Fatalf("replacement did not supersede: %+v", c.Documents)
	}
	submit(t, citizen, c).expect(t, http.StatusAccepted)
	waitFor(t, citizen, c.ID, "UNDER_REVIEW", inState("UNDER_REVIEW"))

	var trail struct {
		Items []struct {
			EventType string         `json:"eventType"`
			Payload   map[string]any `json:"payload"`
		} `json:"items"`
	}
	call(t, citizen, "GET", "/api/v1/cases/"+c.ID+"/events?limit=100", nil, nil).expect(t, http.StatusOK).json(t, &trail)
	resubmitted := false
	for _, e := range trail.Items {
		if e.EventType == "case.state_changed" && e.Payload["transition"] == "T3" && e.Payload["resubmission"] == true {
			resubmitted = true
		}
	}
	if !resubmitted {
		t.Error("no T3 resubmission in the audit trail")
	}
}

func reviewCase(t *testing.T) caseDetail {
	t.Helper()
	c := createCase(t, citizen)
	attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	c = getCase(t, citizen, c.ID)
	submit(t, citizen, c).expect(t, http.StatusAccepted)
	waitFor(t, citizen, c.ID, "UNDER_REVIEW", inState("UNDER_REVIEW"))
	return getCase(t, caseworker, c.ID)
}

func decide(t *testing.T, c caseDetail, decision, reason string) response {
	t.Helper()
	return call(t, caseworker, "POST", "/api/v1/worker/cases/"+c.ID+"/decision",
		map[string]string{"decision": decision, "reason": reason}, idempotent(ifMatch(c.Version)))
}

func TestAC16_AC18_AcceptIsFinal(t *testing.T) {
	c := reviewCase(t)
	var out struct {
		State     string  `json:"state"`
		DecidedAt *string `json:"decidedAt"`
	}
	decide(t, c, "ACCEPTED", "").expect(t, http.StatusOK).json(t, &out)
	if out.State != "ACCEPTED" || out.DecidedAt == nil {
		t.Errorf("got %+v", out)
	}
	c = getCase(t, citizen, c.ID)
	if c.DecidedBy == nil || *c.DecidedBy == "" {
		t.Error("decidedBy not set")
	}
	submit(t, citizen, c).expectProblem(t, http.StatusConflict, "INVALID_STATE_TRANSITION") // AC-18
}

func TestAC17_RejectNeedsReason(t *testing.T) {
	c := reviewCase(t)
	decide(t, c, "REJECTED", "").expectProblem(t, http.StatusUnprocessableEntity, "DECISION_REASON_REQUIRED")
	decide(t, c, "REJECTED", "Plans are not signed").expect(t, http.StatusOK)
}

func TestAC19_StaleVersionIsRejected(t *testing.T) {
	c := createCase(t, citizen)
	attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf") // moves the version on
	submit(t, citizen, c).expectProblem(t, http.StatusConflict, "CONCURRENT_MODIFICATION")
}

func TestAC20_ConcurrentSubmitsOneWins(t *testing.T) {
	c := createCase(t, citizen)
	attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	c = getCase(t, citizen, c.ID)
	var wg sync.WaitGroup
	results := make([]response, 2)
	for i := range results {
		wg.Go(func() { results[i] = submit(t, citizen, c) })
	}
	wg.Wait()
	accepted, conflicts := 0, 0
	for _, r := range results {
		switch r.Status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			conflicts++
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Errorf("%d accepted, %d conflicts; want 1 and 1", accepted, conflicts)
	}
}

func TestAC21_DocumentsImmutableUnderReview(t *testing.T) {
	c := reviewCase(t)
	call(t, citizen, "POST", "/api/v1/cases/"+c.ID+"/documents",
		map[string]any{"filename": "late.pdf", "declaredMime": "application/pdf", "sizeBytes": 10}, nil).
		expectProblem(t, http.StatusConflict, "DOCUMENTS_IMMUTABLE")
}

// AC-22 through the public API: a repeated completion publishes a second
// document.uploaded, yet the document ends up with exactly one result.
func TestAC22_DuplicateEventsYieldOneResult(t *testing.T) {
	c := createCase(t, citizen)
	doc := attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	call(t, citizen, "POST", "/api/v1/cases/"+c.ID+"/documents/"+doc+"/complete", map[string]any{}, idempotent(nil)).
		expect(t, http.StatusOK)
	first := waitFor(t, citizen, c.ID, "validation", validated(doc)).doc(doc).Validation
	again := waitFor(t, citizen, c.ID, "validation", validated(doc)).doc(doc).Validation
	if first.Checks.ContentDigest != again.Checks.ContentDigest {
		t.Error("validation result changed after a duplicate event")
	}
}

func TestAC25_TenDocumentsAllValidated(t *testing.T) {
	c := createCase(t, citizen)
	var wg sync.WaitGroup
	ids := make([]string, 10)
	for i := range ids {
		wg.Go(func() { ids[i] = attach(t, citizen, c.ID, "doc-"+string(rune('a'+i))+".pdf", "valid-1page.pdf") })
	}
	wg.Wait()
	waitFor(t, citizen, c.ID, "ten validations", validated(ids...))
}

// AC-27 and AC-32: correlation ids are echoed, generated when missing and
// recorded in the audit trail.
func TestAC27_AC32_CorrelationAndAudit(t *testing.T) {
	r := call(t, citizen, "POST", "/api/v1/cases", map[string]string{"caseType": "BENEFIT_CLAIM", "title": "corr"},
		map[string]string{"X-Correlation-Id": "e2e-correlation-1"}).expect(t, http.StatusCreated)
	if r.Header.Get("X-Correlation-Id") != "e2e-correlation-1" {
		t.Errorf("correlation id not echoed: %q", r.Header.Get("X-Correlation-Id"))
	}
	var c caseDetail
	r.json(t, &c)
	var trail struct {
		Items []struct {
			EventType     string `json:"eventType"`
			CorrelationID string `json:"correlationId"`
		} `json:"items"`
	}
	call(t, citizen, "GET", "/api/v1/cases/"+c.ID+"/events", nil, nil).expect(t, http.StatusOK).json(t, &trail)
	if len(trail.Items) != 1 || trail.Items[0].EventType != "case.created" || trail.Items[0].CorrelationID != "e2e-correlation-1" {
		t.Errorf("audit trail %+v", trail.Items)
	}
	if id := call(t, citizen, "GET", "/api/v1/cases/"+c.ID, nil, nil).Header.Get("X-Correlation-Id"); id == "" {
		t.Error("no correlation id generated")
	}
}

func TestAC35_AuditTrailIsPaginated(t *testing.T) {
	c := createCase(t, citizen)
	attach(t, citizen, c.ID, "a.pdf", "valid-1page.pdf")
	attach(t, citizen, c.ID, "b.pdf", "valid-1page.pdf")
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"nextCursor"`
	}
	call(t, citizen, "GET", "/api/v1/cases/"+c.ID+"/events?limit=2", nil, nil).expect(t, http.StatusOK).json(t, &page)
	if len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("first page %+v", page)
	}
	call(t, citizen, "GET", "/api/v1/cases/"+c.ID+"/events?limit=2&cursor="+*page.NextCursor, nil, nil).expect(t, http.StatusOK).json(t, &page)
	if len(page.Items) == 0 {
		t.Error("second page empty")
	}
	// A garbled cursor is the caller's mistake and is reported without internals.
	r := call(t, citizen, "GET", "/api/v1/cases?cursor=not-a-cursor", nil, nil)
	r.expectProblem(t, http.StatusBadRequest, "VALIDATION_FAILED")
	if strings.Contains(string(r.Body), "base64") || strings.Contains(string(r.Body), "illegal") {
		t.Errorf("cursor error leaks internals: %s", r.Body)
	}
}

func TestAC36_CaseworkerFiltersByState(t *testing.T) {
	reviewCase(t)
	var page struct {
		Items []struct {
			State string `json:"state"`
		} `json:"items"`
	}
	call(t, caseworker, "GET", "/api/v1/worker/cases?state=UNDER_REVIEW&limit=100", nil, nil).expect(t, http.StatusOK).json(t, &page)
	if len(page.Items) == 0 {
		t.Fatal("no case under review listed")
	}
	for _, it := range page.Items {
		if it.State != "UNDER_REVIEW" {
			t.Errorf("listed a case in %s", it.State)
		}
	}
}

// FR-306: the download URL returns exactly the uploaded bytes.
func TestDownloadReturnsUploadedBytes(t *testing.T) {
	c := createCase(t, citizen)
	doc := attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	var out struct {
		DownloadURL string `json:"downloadUrl"`
	}
	call(t, citizen, "GET", "/api/v1/cases/"+c.ID+"/documents/"+doc+"/download", nil, nil).expect(t, http.StatusOK).json(t, &out)
	resp, err := http.Get(out.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, fixture(t, "valid-1page.pdf")) {
		t.Error("downloaded bytes differ from the upload")
	}
	call(t, citizen2, "GET", "/api/v1/cases/"+c.ID+"/documents/"+doc+"/download", nil, nil).
		expectProblem(t, http.StatusNotFound, "DOCUMENT_NOT_FOUND")
}

// FR-308: deleting a document in DRAFT, with If-Match, idempotently.
func TestDeleteDocument(t *testing.T) {
	c := createCase(t, citizen)
	doc := attach(t, citizen, c.ID, "plan.pdf", "valid-1page.pdf")
	c = getCase(t, citizen, c.ID)
	path := "/api/v1/cases/" + c.ID + "/documents/" + doc
	call(t, citizen, "DELETE", path, nil, nil).expectProblem(t, http.StatusBadRequest, "VALIDATION_FAILED") // no If-Match
	call(t, citizen, "DELETE", path, nil, ifMatch(c.Version)).expect(t, http.StatusNoContent)
	if d := getCase(t, citizen, c.ID).doc(doc); d.Status != "DELETED" {
		t.Errorf("status %q, want DELETED", d.Status)
	}
}

// FR-214: caseworkers can export a case as JSON Lines.
func TestExportForCaseworkers(t *testing.T) {
	c := reviewCase(t)
	r := call(t, caseworker, "GET", "/api/v1/worker/cases/"+c.ID+"/export", nil, nil).expect(t, http.StatusOK)
	lines := strings.Split(strings.TrimSpace(string(r.Body)), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], `"kind":"case"`) {
		t.Errorf("export %q", r.Body)
	}
}
