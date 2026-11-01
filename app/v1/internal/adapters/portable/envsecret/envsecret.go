// Package envsecret is the portable SecretProvider adapter (SRS §7.3): secrets
// that Kubernetes injects as environment variables or mounted files.
package envsecret

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Provider implements ports.SecretProvider.
type Provider struct{}

var _ ports.SecretProvider = Provider{}

// Get returns the variable called name (e.g. CCSRA_DB_PASSWORD), or the
// contents of the file named by name+"_FILE" (a mounted Kubernetes secret).
func (Provider) Get(_ context.Context, name string) (string, error) {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v, nil
	}
	if path, ok := os.LookupEnv(name + "_FILE"); ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("secret %s: %w", name, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", fmt.Errorf("secret %s: %w", name, ports.ErrNotFound)
}
