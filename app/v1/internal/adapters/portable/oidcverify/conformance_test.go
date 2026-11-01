package oidcverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports/conformance"
)

// TestConformance runs the IdentityVerifier suite against the local Keycloak.
// Client and admin credentials are the local test values of
// deploy/local/keycloak/ccsra-realm.json and docker-compose.yaml.
func TestConformance(t *testing.T) {
	conformance.RequireEnabled(t)
	base := env("CCSRA_KEYCLOAK_URL", "http://localhost:8180")
	issuer := base + "/realms/ccsra"
	kc := keycloak{base: base, realm: "ccsra"}

	conformance.TestIdentityVerifier(t, conformance.IdentityVerifierFixture{
		Params: conformance.VerifierParams{
			Issuer:   issuer,
			Audience: "ccsra-api",
			JWKSURL:  issuer + "/protocol/openid-connect/certs",
		},
		New: func(p conformance.VerifierParams) (ports.IdentityVerifier, error) {
			return New(context.Background(), Config{
				Issuer: p.Issuer, Audience: p.Audience, JWKSURL: p.JWKSURL, Now: p.Now,
				MinRefreshInterval: 100 * time.Millisecond,
			})
		},
		Token: func(ctx context.Context) (string, error) {
			return kc.token(ctx, "ccsra", url.Values{
				"grant_type":    {"client_credentials"},
				"client_id":     {"ccsra-harness-citizen"},
				"client_secret": {env("CCSRA_HARNESS_CITIZEN_SECRET", "local-harness-citizen-secret")},
			})
		},
		WantRole:  "citizen",
		RotateKey: kc.rotateKey,
	})
}

type keycloak struct {
	base  string
	realm string
}

func (k keycloak) token(ctx context.Context, realm string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.base+"/realms/"+realm+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := k.do(req, http.StatusOK, &out); err != nil {
		return "", err
	}
	return out.AccessToken, nil
}

// rotateKey adds an RSA key provider with a higher priority than the realm's
// default one, so Keycloak signs new tokens with a new key.
func (k keycloak) rotateKey(ctx context.Context) (func(), error) {
	admin, err := k.token(ctx, "master", url.Values{
		"grant_type": {"password"}, "client_id": {"admin-cli"},
		"username": {"admin"}, "password": {env("CCSRA_KEYCLOAK_ADMIN_PASSWORD", "admin-local")},
	})
	if err != nil {
		return nil, err
	}
	authed := func(method, u string, body any) *http.Request {
		var b strings.Builder
		if body != nil {
			_ = json.NewEncoder(&b).Encode(body)
		}
		req, _ := http.NewRequestWithContext(ctx, method, u, strings.NewReader(b.String()))
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	var realm struct {
		ID string `json:"id"`
	}
	if err := k.do(authed(http.MethodGet, k.base+"/admin/realms/"+k.realm, nil), http.StatusOK, &realm); err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(authed(http.MethodPost, k.base+"/admin/realms/"+k.realm+"/components", map[string]any{
		"name":         fmt.Sprintf("conformance-rotation-%d", time.Now().UnixNano()),
		"providerId":   "rsa-generated",
		"providerType": "org.keycloak.keys.KeyProvider",
		"parentId":     realm.ID,
		"config": map[string][]string{
			"priority": {"1000"}, "enabled": {"true"}, "active": {"true"}, "algorithm": {"RS256"}, "keySize": {"2048"},
		},
	}))
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create key provider: status %d", resp.StatusCode)
	}
	componentID := path.Base(resp.Header.Get("Location"))
	return func() {
		_ = k.do(authed(http.MethodDelete, k.base+"/admin/realms/"+k.realm+"/components/"+componentID, nil), http.StatusNoContent, nil)
	}, nil
}

func (k keycloak) do(req *http.Request, want int, out any) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
