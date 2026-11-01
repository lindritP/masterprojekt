package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// DocumentStoreFixture provides a live DocumentStore adapter.
type DocumentStoreFixture struct {
	Store ports.DocumentStore
}

const pdfType = "application/pdf"

// TestDocumentStore runs the DocumentStore semantics of SRS §7.4.
func TestDocumentStore(t *testing.T, f DocumentStoreFixture) {
	ctx := context.Background()
	s := f.Store
	run := RunID()
	key := func(name string) string { return fmt.Sprintf("conformance/%s/%s", run, name) }
	limits := ports.PutConstraints{ContentType: pdfType, MaxSizeBytes: 1024, TTL: time.Minute}

	t.Run("PresignUpload_EnforcesContentType", func(t *testing.T) {
		u, err := s.PresignUpload(ctx, key("ct"), limits)
		skipIfUnsupported(t, err, "presigned upload")
		mustOK(t, err)
		if got := upload(t, u, "image/png", 100); got < 400 || got >= 500 {
			t.Errorf("upload with wrong Content-Type: status %d, want 4xx", got)
		}
		if got := upload(t, u, pdfType, 100); got >= 300 {
			t.Errorf("upload with correct Content-Type: status %d, want 2xx", got)
		}
	})

	t.Run("PresignUpload_EnforcesMaxSize", func(t *testing.T) {
		u, err := s.PresignUpload(ctx, key("size"), limits)
		skipIfUnsupported(t, err, "presigned upload")
		mustOK(t, err)
		if got := upload(t, u, pdfType, int(limits.MaxSizeBytes)+1); got < 400 || got >= 500 {
			t.Errorf("upload over MaxSizeBytes: status %d, want 4xx", got)
		}
		if _, err := s.Head(ctx, key("size")); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("rejected upload left an object behind (Head err = %v)", err)
		}
	})

	t.Run("PresignUpload_Expires", func(t *testing.T) {
		short := limits
		short.TTL = time.Second
		u, err := s.PresignUpload(ctx, key("expiry"), short)
		skipIfUnsupported(t, err, "presigned upload")
		mustOK(t, err)
		time.Sleep(2 * time.Second)
		if got := upload(t, u, pdfType, 100); got < 400 || got >= 500 {
			t.Errorf("upload after TTL: status %d, want 4xx", got)
		}
	})

	t.Run("PresignGet_ReturnsExactBytes", func(t *testing.T) {
		body := pattern(777)
		put(t, s, key("bytes"), limits, body)
		u, err := s.PresignGet(ctx, key("bytes"), time.Minute)
		mustOK(t, err)
		resp, err := http.Get(u.URL)
		mustOK(t, err)
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		mustOK(t, err)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
			t.Errorf("GET returned status %d and %d bytes, want 200 and the %d uploaded bytes", resp.StatusCode, len(got), len(body))
		}
	})

	t.Run("Head_ReportsExactSize", func(t *testing.T) {
		put(t, s, key("head"), limits, pattern(321))
		info, err := s.Head(ctx, key("head"))
		mustOK(t, err)
		if info.SizeBytes != 321 {
			t.Errorf("Head size = %d, want 321", info.SizeBytes)
		}
		if _, err := s.Head(ctx, key("missing")); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("Head of a missing key: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Delete_IsIdempotent", func(t *testing.T) {
		put(t, s, key("delete"), limits, pattern(10))
		mustOK(t, s.Delete(ctx, key("delete")))
		if _, err := s.Head(ctx, key("delete")); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("object still present after Delete (Head err = %v)", err)
		}
		if err := s.Delete(ctx, key("delete")); err != nil {
			t.Errorf("second Delete = %v, want nil", err)
		}
		if err := s.Delete(ctx, key("never-existed")); err != nil {
			t.Errorf("Delete of a never-existing key = %v, want nil", err)
		}
	})

	t.Run("ApplyRetention_BlocksDeleteOrReportsUnsupported", func(t *testing.T) {
		put(t, s, key("retained"), limits, pattern(10))
		err := s.ApplyRetention(ctx, key("retained"), time.Now().Add(time.Minute))
		skipIfUnsupported(t, err, "object retention")
		mustOK(t, err)
		if err := s.Delete(ctx, key("retained")); err == nil {
			t.Error("Delete of a retained object succeeded; retention was silently not enforced")
		}
		if _, err := s.Head(ctx, key("retained")); err != nil {
			t.Errorf("retained object is gone (Head err = %v)", err)
		}
	})
}

// upload sends size bytes through a presigned upload with the given
// Content-Type and returns the HTTP status.
func upload(t *testing.T, u ports.PresignedURL, contentType string, size int) int {
	t.Helper()
	var req *http.Request
	var err error
	switch u.Method {
	case http.MethodPut:
		req, err = http.NewRequest(http.MethodPut, u.URL, bytes.NewReader(pattern(size)))
		mustOK(t, err)
		for k, v := range u.RequiredHeaders {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", contentType)
	case http.MethodPost:
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for k, v := range u.FormFields {
			if k != "Content-Type" {
				mustOK(t, w.WriteField(k, v))
			}
		}
		mustOK(t, w.WriteField("Content-Type", contentType))
		fw, err := w.CreateFormFile("file", "document.pdf")
		mustOK(t, err)
		_, err = fw.Write(pattern(size))
		mustOK(t, err)
		mustOK(t, w.Close())
		req, err = http.NewRequest(http.MethodPost, u.URL, &b)
		mustOK(t, err)
		req.Header.Set("Content-Type", w.FormDataContentType())
	default:
		t.Fatalf("unexpected upload method %q", u.Method)
	}
	resp, err := http.DefaultClient.Do(req)
	mustOK(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// put stores body under key through the store's own presigned upload.
func put(t *testing.T, s ports.DocumentStore, key string, c ports.PutConstraints, body []byte) {
	t.Helper()
	u, err := s.PresignUpload(context.Background(), key, c)
	mustOK(t, err)
	var req *http.Request
	if u.Method == http.MethodPut {
		req, err = http.NewRequest(http.MethodPut, u.URL, bytes.NewReader(body))
		mustOK(t, err)
		for k, v := range u.RequiredHeaders {
			req.Header.Set(k, v)
		}
	} else {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for k, v := range u.FormFields {
			mustOK(t, w.WriteField(k, v))
		}
		fw, err := w.CreateFormFile("file", "document.pdf")
		mustOK(t, err)
		_, err = fw.Write(body)
		mustOK(t, err)
		mustOK(t, w.Close())
		req, err = http.NewRequest(http.MethodPost, u.URL, &b)
		mustOK(t, err)
		req.Header.Set("Content-Type", w.FormDataContentType())
	}
	resp, err := http.DefaultClient.Do(req)
	mustOK(t, err)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("setup upload of %s failed with status %d", key, resp.StatusCode)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 31)
	}
	return b
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
