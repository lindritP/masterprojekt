package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The suite is parameterised only by base URL and credentials (FR-044). The
// defaults are the local stack's test values (deploy/local/).
var (
	baseURL  = env("CCSRA_E2E_BASE_URL", "http://localhost:8080")
	tokenURL = env("CCSRA_E2E_TOKEN_URL", "http://localhost:8180/realms/ccsra/protocol/openid-connect/token")

	citizen    = &principal{id: env("CCSRA_E2E_CITIZEN_CLIENT", "ccsra-harness-citizen"), secret: env("CCSRA_E2E_CITIZEN_SECRET", "local-harness-citizen-secret")}
	citizen2   = &principal{id: env("CCSRA_E2E_CITIZEN2_CLIENT", "ccsra-harness-citizen-2"), secret: env("CCSRA_E2E_CITIZEN2_SECRET", "local-harness-citizen-2-secret")}
	caseworker = &principal{id: env("CCSRA_E2E_CASEWORKER_CLIENT", "ccsra-harness-caseworker"), secret: env("CCSRA_E2E_CASEWORKER_SECRET", "local-harness-caseworker-secret")}
)

const corpusDir = "../corpus"

func TestMain(m *testing.M) {
	if os.Getenv("CCSRA_E2E") != "1" {
		fmt.Println("skipping E2E suite: set CCSRA_E2E=1 and start the application (make dev)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// principal is a client-credentials identity of the harness (FR-106).
type principal struct {
	id, secret string
	mu         sync.Mutex
	token      string
	expires    time.Time
}

func (p *principal) bearer(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.expires) {
		return p.token
	}
	resp, err := http.PostForm(tokenURL, url.Values{
		"grant_type": {"client_credentials"}, "client_id": {p.id}, "client_secret": {p.secret},
	})
	if err != nil {
		t.Fatalf("token for %s: %v", p.id, err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		t.Fatalf("token for %s: status %d", p.id, resp.StatusCode)
	}
	p.token, p.expires = out.AccessToken, time.Now().Add(time.Duration(out.ExpiresIn-30)*time.Second)
	return p.token
}

// response is a decoded API response.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

// code returns the problem code of an error response.
func (r response) code() string {
	var p struct{ Code string }
	_ = json.Unmarshal(r.Body, &p)
	return p.Code
}

func (r response) expect(t *testing.T, status int) response {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d: %s", r.Status, status, r.Body)
	}
	return r
}

func (r response) expectProblem(t *testing.T, status int, code string) {
	t.Helper()
	if r.Status != status || r.code() != code {
		t.Fatalf("got %d %s, want %d %s: %s", r.Status, r.code(), status, code, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("problem served as %q", ct)
	}
}

// call sends a request to the public API as p (nil: no token).
func call(t *testing.T, p *principal, method, path string, body any, header map[string]string) response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		req.Header.Set("Authorization", "Bearer "+p.bearer(t))
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

// ---------------------------------------------------------------- representations

type caseDetail struct {
	ID             string     `json:"id"`
	Reference      string     `json:"reference"`
	State          string     `json:"state"`
	Version        int        `json:"version"`
	SubmittedAt    *string    `json:"submittedAt"`
	DecidedAt      *string    `json:"decidedAt"`
	DecidedBy      *string    `json:"decidedBy"`
	DecisionReason *string    `json:"decisionReason"`
	Documents      []document `json:"documents"`
}

type document struct {
	ID           string  `json:"id"`
	Filename     string  `json:"filename"`
	Status       string  `json:"status"`
	SupersedesID *string `json:"supersedesId"`
	SHA256       *string `json:"sha256"`
	Validation   *struct {
		Outcome      string   `json:"outcome"`
		FailureCodes []string `json:"failureCodes"`
		Checks       struct {
			PageCount     int    `json:"pageCount"`
			ContentDigest string `json:"contentDigest"`
			ThumbnailKey  string `json:"thumbnailKey"`
		} `json:"checks"`
	} `json:"validation"`
}

func (c caseDetail) doc(id string) document {
	for _, d := range c.Documents {
		if d.ID == id {
			return d
		}
	}
	return document{}
}

type upload struct {
	DocumentID      string            `json:"documentId"`
	UploadURL       string            `json:"uploadUrl"`
	UploadMethod    string            `json:"uploadMethod"`
	RequiredHeaders map[string]string `json:"requiredHeaders"`
	FormFields      map[string]string `json:"formFields"`
	ExpiresAt       string            `json:"expiresAt"`
}

// ---------------------------------------------------------------- flows

func ifMatch(version int) map[string]string {
	return map[string]string{"If-Match": fmt.Sprintf("%q", fmt.Sprint(version))}
}

var keySeq struct {
	sync.Mutex
	n int
}

func key() string {
	keySeq.Lock()
	defer keySeq.Unlock()
	keySeq.n++
	return fmt.Sprintf("e2e-%d-%d", time.Now().UnixNano(), keySeq.n)
}

func idempotent(extra map[string]string) map[string]string {
	h := map[string]string{"Idempotency-Key": key()}
	maps.Copy(h, extra)
	return h
}

func createCase(t *testing.T, p *principal) caseDetail {
	t.Helper()
	var c caseDetail
	call(t, p, "POST", "/api/v1/cases", map[string]string{"caseType": "BUILDING_PERMIT", "title": "E2E " + t.Name()}, nil).
		expect(t, http.StatusCreated).json(t, &c)
	return c
}

func getCase(t *testing.T, p *principal, id string) caseDetail {
	t.Helper()
	var c caseDetail
	call(t, p, "GET", "/api/v1/cases/"+id, nil, nil).expect(t, http.StatusOK).json(t, &c)
	return c
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name))
	if err != nil {
		t.Fatalf("%v (run `make corpus`)", err)
	}
	return b
}

// register asks for an upload slot for data under filename.
func register(t *testing.T, p *principal, caseID, filename string, size int) upload {
	t.Helper()
	var u upload
	call(t, p, "POST", "/api/v1/cases/"+caseID+"/documents",
		map[string]any{"filename": filename, "declaredMime": "application/pdf", "sizeBytes": size}, nil).
		expect(t, http.StatusCreated).json(t, &u)
	return u
}

// put sends data through a presigned upload with the given Content-Type and
// returns the storage service's status.
func put(t *testing.T, u upload, contentType string, data []byte) int {
	t.Helper()
	var req *http.Request
	var err error
	if u.UploadMethod == "PUT" {
		req, err = http.NewRequest("PUT", u.UploadURL, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range u.RequiredHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", contentType)
	} else {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for k, v := range u.FormFields {
			if k != "Content-Type" {
				_ = w.WriteField(k, v)
			}
		}
		_ = w.WriteField("Content-Type", contentType)
		fw, _ := w.CreateFormFile("file", "document.pdf")
		_, _ = fw.Write(data)
		_ = w.Close()
		req, err = http.NewRequest("POST", u.UploadURL, &b)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", w.FormDataContentType())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// attach registers, uploads and completes a corpus file; returns the document id.
func attach(t *testing.T, p *principal, caseID, filename, fixtureName string) string {
	t.Helper()
	data := fixture(t, fixtureName)
	u := register(t, p, caseID, filename, len(data))
	if s := put(t, u, "application/pdf", data); s >= 300 {
		t.Fatalf("upload rejected by storage: %d", s)
	}
	call(t, p, "POST", "/api/v1/cases/"+caseID+"/documents/"+u.DocumentID+"/complete", map[string]any{}, idempotent(nil)).
		expect(t, http.StatusOK)
	return u.DocumentID
}

func submit(t *testing.T, p *principal, c caseDetail) response {
	t.Helper()
	return call(t, p, "POST", "/api/v1/cases/"+c.ID+"/submit", nil, idempotent(ifMatch(c.Version)))
}

// waitFor polls the case until cond holds (5 s per AC-12, plus slack for a cold worker).
func waitFor(t *testing.T, p *principal, id string, what string, cond func(caseDetail) bool) caseDetail {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		c := getCase(t, p, id)
		if cond(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; case is %s: %+v", what, c.State, c.Documents)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func inState(states ...string) func(caseDetail) bool {
	return func(c caseDetail) bool { return slices.Contains(states, c.State) }
}

// validated reports whether every listed document has a validation result.
func validated(ids ...string) func(caseDetail) bool {
	return func(c caseDetail) bool {
		for _, id := range ids {
			if c.doc(id).Validation == nil {
				return false
			}
		}
		return true
	}
}
