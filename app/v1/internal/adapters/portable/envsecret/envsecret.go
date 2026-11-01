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

// Get returns CCSRA_SECRET_<NAME>, or the contents of the file named by
// CCSRA_SECRET_<NAME>_FILE. NAME is upper-cased with dashes as underscores.
func (Provider) Get(_ context.Context, name string) (string, error) {
	key := "CCSRA_SECRET_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if v, ok := os.LookupEnv(key); ok {
		return v, nil
	}
	if path, ok := os.LookupEnv(key + "_FILE"); ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("secret %s: %w", name, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", fmt.Errorf("secret %s: %w", name, ports.ErrNotFound)
}
