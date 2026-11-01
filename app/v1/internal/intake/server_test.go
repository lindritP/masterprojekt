package intake

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// fakeStore signs anything; the tests are about what intake lets through.
type fakeStore struct{ ports.DocumentStore }

func (fakeStore) PresignGet(context.Context, string, time.Duration) (ports.PresignedURL, error) {
	return ports.PresignedURL{URL: "http://store/get", Method: "GET"}, nil
}

func (fakeStore) PresignUpload(context.Context, string, ports.PutConstraints) (ports.PresignedURL, error) {
	return ports.PresignedURL{URL: "http://store/up", Method: "POST"}, nil
}

func TestPresignForWorker_OnlyOriginalsAndThumbnails(t *testing.T) {
	srv := &Server{Internal: fakeStore{}, MaxObjectBytes: 20 << 20, MaxBodyBytes: 4096, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	srv.Routes(mux)
	handler := platform.InternalContext(mux)

	const doc = "cases/01a1976c-de50-790a-bbd3-1218e2a86582/documents/01a1976c-de57-7cfa-8f1e-5b9f7d5c2a10"
	for _, tc := range []struct {
		name, caller, body string
		want               int
	}{
		{"read original", "validation-worker", `{"key":"` + doc + `/original","operation":"GET"}`, 200},
		{"write thumbnail", "validation-worker", `{"key":"` + doc + `/thumbnail.png","operation":"UPLOAD","contentType":"image/png","maxSizeBytes":1000}`, 200},
		{"overwrite original", "validation-worker", `{"key":"` + doc + `/original","operation":"UPLOAD","contentType":"image/png","maxSizeBytes":1000}`, 400},
		{"thumbnail as pdf", "validation-worker", `{"key":"` + doc + `/thumbnail.png","operation":"UPLOAD","contentType":"application/pdf","maxSizeBytes":1000}`, 400},
		{"oversized thumbnail", "validation-worker", `{"key":"` + doc + `/thumbnail.png","operation":"UPLOAD","contentType":"image/png","maxSizeBytes":2000000}`, 400},
		{"other key", "validation-worker", `{"key":"cases/../secret","operation":"GET"}`, 400},
		{"not the worker", "identity-gateway", `{"key":"` + doc + `/original","operation":"GET"}`, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/internal/v1/objects/presign", strings.NewReader(tc.body))
			req.Header.Set(platform.HeaderService, tc.caller)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}
