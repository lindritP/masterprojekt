// Package identity selects the IdentityVerifier adapter (CCSRA_ADAPTER_IDENTITY).
package identity

import (
	"context"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/adapters/portable/oidcverify"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Setup is a validated configuration, ready to build a verifier.
type Setup struct {
	Name string
	cfg  oidcverify.Config
}

// FromEnv reads CCSRA_ADAPTER_IDENTITY, CCSRA_OIDC_* and CCSRA_CLAIM_*.
func FromEnv(e *platform.Env) Setup {
	name := e.Adapter("CCSRA_ADAPTER_IDENTITY", "oidc")
	return Setup{Name: name, cfg: oidcverify.Config{
		Issuer:       e.Required("CCSRA_OIDC_ISSUER"),
		Audience:     e.Required("CCSRA_OIDC_AUDIENCE"),
		JWKSURL:      e.String("CCSRA_OIDC_JWKS_URL", ""),
		JWKSTTL:      e.Seconds("CCSRA_OIDC_JWKS_TTL_SECONDS", 3600),
		SubjectClaim: e.String("CCSRA_CLAIM_SUBJECT", "sub"),
		RolesClaim:   e.String("CCSRA_CLAIM_ROLES", "roles"),
	}}
}

// Open builds the verifier (discovering the JWKS URL if needed).
func (s Setup) Open(ctx context.Context) (ports.IdentityVerifier, error) {
	return oidcverify.New(ctx, s.cfg)
}
