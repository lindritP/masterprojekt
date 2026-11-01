// Package repository selects the CaseRepository adapter (CCSRA_ADAPTER_REPOSITORY).
package repository

import (
	"context"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/adapters/portable/pgrepo"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Repository is a CaseRepository that can report readiness and be closed.
type Repository interface {
	ports.CaseRepository
	Ping(ctx context.Context) error
	Close()
}

// Setup is a validated configuration, ready to connect.
type Setup struct {
	Name string
	cfg  pgrepo.Config
}

// FromEnv reads CCSRA_ADAPTER_REPOSITORY and CCSRA_DB_* (SRS §9.3).
func FromEnv(ctx context.Context, e *platform.Env, secrets ports.SecretProvider) Setup {
	name := e.Adapter("CCSRA_ADAPTER_REPOSITORY", "pg")
	cfg := pgrepo.Config{
		Host:     e.Required("CCSRA_DB_HOST"),
		Port:     e.Int("CCSRA_DB_PORT", 5432),
		Name:     e.Required("CCSRA_DB_NAME"),
		User:     e.Required("CCSRA_DB_USER"),
		SSLMode:  e.String("CCSRA_DB_SSLMODE", "require"),
		MaxConns: e.Int("CCSRA_DB_MAX_CONNS", 20),
	}
	if mode := e.String("CCSRA_DB_AUTH_MODE", "password"); mode == "password" {
		cfg.Password = e.Secret(ctx, secrets, "CCSRA_DB_PASSWORD")
	} else {
		e.Problem("CCSRA_DB_AUTH_MODE=%q is not supported by the %q adapter (IAM authentication is the pg-iam adapter, stage B6)", mode, name)
	}
	return Setup{Name: name, cfg: cfg}
}

// Open connects.
func (s Setup) Open(ctx context.Context) (Repository, error) {
	return pgrepo.New(ctx, s.cfg)
}
