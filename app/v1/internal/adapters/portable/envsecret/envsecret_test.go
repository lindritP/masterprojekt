package envsecret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

func TestGet(t *testing.T) {
	ctx := context.Background()
	t.Setenv("CCSRA_DB_PASSWORD", "from-env")
	file := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CCSRA_BROKER_PASSWORD_FILE", file)

	for name, want := range map[string]string{"CCSRA_DB_PASSWORD": "from-env", "CCSRA_BROKER_PASSWORD": "from-file"} {
		if got, err := (Provider{}).Get(ctx, name); err != nil || got != want {
			t.Errorf("Get(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := (Provider{}).Get(ctx, "CCSRA_MISSING"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("missing secret: err = %v, want ErrNotFound", err)
	}
}
