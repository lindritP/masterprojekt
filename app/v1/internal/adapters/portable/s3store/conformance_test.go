package s3store

import (
	"os"
	"testing"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports/conformance"
)

// TestConformance runs the DocumentStore suite against the local SeaweedFS
// (or any S3 endpoint given through the environment).
func TestConformance(t *testing.T) {
	conformance.RequireEnabled(t)
	s, err := New(Config{
		Endpoint:  env("CCSRA_STORAGE_ENDPOINT", "http://localhost:8333"),
		Region:    env("CCSRA_STORAGE_REGION", "eu-central-1"),
		Bucket:    env("CCSRA_STORAGE_BUCKET", "ccsra-documents"),
		AccessKey: env("CCSRA_STORAGE_ACCESS_KEY", "ccsra-local"),
		SecretKey: env("CCSRA_STORAGE_SECRET_KEY", "ccsra-local-secret"),
		PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	conformance.TestDocumentStore(t, conformance.DocumentStoreFixture{Store: s})
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
