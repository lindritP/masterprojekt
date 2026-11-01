// Package secrets selects the SecretProvider adapter (CCSRA_ADAPTER_SECRETS).
package secrets

import (
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/adapters/portable/envsecret"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// FromEnv returns the configured provider and its adapter name.
func FromEnv(e *platform.Env) (ports.SecretProvider, string) {
	name := e.Adapter("CCSRA_ADAPTER_SECRETS", "k8s")
	return envsecret.Provider{}, name
}
