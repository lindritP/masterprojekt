// Package oidcverify is the portable IdentityVerifier adapter (SRS §7.3):
// generic OIDC/JWT validation against any issuer's JWKS, tested with Keycloak.
//
// The key set is cached for a TTL and refreshed early when a token names an
// unknown key id, at most once per MinRefreshInterval (FR-102). Claim names
// are configuration (CCSRA_CLAIM_SUBJECT, CCSRA_CLAIM_ROLES), which is where
// identity coupling concentrates (FR-103).
package oidcverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Config mirrors the CCSRA_OIDC_* and CCSRA_CLAIM_* variables (SRS §9.3).
type Config struct {
	Issuer             string
	Audience           string
	JWKSURL            string        // discovered from the issuer if empty
	JWKSTTL            time.Duration // default 1h
	JWKSMaxStale       time.Duration // cached keys are rejected after this age, even if the issuer is down; default 6h
	MinRefreshInterval time.Duration // default 10s
	SubjectClaim       string        // default "sub"
	RolesClaim         string        // default "roles"; dots address nested claims
	Now                func() time.Time
	HTTPClient         *http.Client
}

// Verifier implements ports.IdentityVerifier.
type Verifier struct {
	cfg      Config
	verifier *oidc.IDTokenVerifier
}

var _ ports.IdentityVerifier = (*Verifier)(nil)

var signingAlgs = []jose.SignatureAlgorithm{jose.RS256, jose.PS256, jose.ES256}

func New(ctx context.Context, cfg Config) (*Verifier, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("oidcverify: issuer and audience are required")
	}
	if cfg.JWKSTTL == 0 {
		cfg.JWKSTTL = time.Hour
	}
	if cfg.JWKSMaxStale == 0 {
		cfg.JWKSMaxStale = 6 * time.Hour
	}
	if cfg.MinRefreshInterval == 0 {
		cfg.MinRefreshInterval = 10 * time.Second
	}
	if cfg.SubjectClaim == "" {
		cfg.SubjectClaim = "sub"
	}
	if cfg.RolesClaim == "" {
		cfg.RolesClaim = "roles"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	if cfg.JWKSURL == "" {
		u, err := discoverJWKS(ctx, cfg.HTTPClient, cfg.Issuer)
		if err != nil {
			return nil, err
		}
		cfg.JWKSURL = u
	}
	algs := make([]string, len(signingAlgs))
	for i, a := range signingAlgs {
		algs[i] = string(a)
	}
	keys := &keySet{cfg: cfg}
	return &Verifier{
		cfg:      cfg,
		verifier: oidc.NewVerifier(cfg.Issuer, keys, &oidc.Config{ClientID: cfg.Audience, Now: cfg.Now, SupportedSigningAlgs: algs}),
	}, nil
}

// Verify checks signature, iss, aud, exp and nbf, then maps the claims onto
// the internal Subject.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (ports.Subject, error) {
	tok, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return ports.Subject{}, fmt.Errorf("%w: %v", ports.ErrUnauthenticated, err)
	}
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return ports.Subject{}, fmt.Errorf("%w: %v", ports.ErrUnauthenticated, err)
	}
	if nbf, ok := claims["nbf"].(float64); ok && v.cfg.Now().Before(time.Unix(int64(nbf), 0).Add(-time.Minute)) {
		return ports.Subject{}, fmt.Errorf("%w: token not valid yet", ports.ErrUnauthenticated)
	}
	sub, _ := lookup(claims, v.cfg.SubjectClaim).(string)
	if sub == "" {
		return ports.Subject{}, fmt.Errorf("%w: no %q claim", ports.ErrUnauthenticated, v.cfg.SubjectClaim)
	}
	acr, _ := claims["acr"].(string)
	return ports.Subject{ID: sub, Roles: stringList(lookup(claims, v.cfg.RolesClaim)), AuthLevel: acr}, nil
}

// lookup resolves a claim name; dots address nested objects (e.g. realm_access.roles).
func lookup(claims map[string]any, name string) any {
	if v, ok := claims[name]; ok {
		return v
	}
	var cur any = claims
	for part := range strings.SplitSeq(name, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func stringList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func discoverJWKS(ctx context.Context, client *http.Client, issuer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc discovery: status %d", resp.StatusCode)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || doc.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery: no jwks_uri (%v)", err)
	}
	return doc.JWKSURI, nil
}

// keySet implements oidc.KeySet with a TTL cache (FR-102). Every refresh —
// for an expired TTL or an unknown key id — is limited to one attempt per
// MinRefreshInterval. While the issuer cannot be reached, the cached keys keep
// being served (stale-while-error), but never beyond JWKSMaxStale, so a
// revoked key cannot live on indefinitely. The HTTP fetch happens outside the
// lock and on a context the caller cannot cancel, so neither a slow issuer
// nor a client that disconnects early can block refreshes for everyone.
type keySet struct {
	cfg Config

	mu          sync.Mutex
	keys        []jose.JSONWebKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

func (k *keySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	jws, err := jose.ParseSignedCompact(raw, signingAlgs)
	if err != nil {
		return nil, err
	}
	if len(jws.Signatures) != 1 {
		return nil, errors.New("expected exactly one signature")
	}
	key, err := k.key(ctx, jws.Signatures[0].Header.KeyID)
	if err != nil {
		return nil, err
	}
	return jws.Verify(key.Key)
}

func (k *keySet) key(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	k.mu.Lock()
	now := k.cfg.Now()
	key, found := k.usable(kid, now)
	stale := k.fetchedAt.IsZero() || now.Sub(k.fetchedAt) > k.cfg.JWKSTTL
	mayRefresh := k.lastAttempt.IsZero() || now.Sub(k.lastAttempt) >= k.cfg.MinRefreshInterval
	if (found && !stale) || !mayRefresh {
		k.mu.Unlock()
		if found {
			return key, nil
		}
		return jose.JSONWebKey{}, fmt.Errorf("unknown key id %q (refresh rate-limited)", kid)
	}
	k.lastAttempt = now
	k.mu.Unlock()

	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	keys, err := k.fetch(fctx)

	k.mu.Lock()
	defer k.mu.Unlock()
	if err == nil {
		k.keys, k.fetchedAt = keys, now
	}
	if key, ok := k.usable(kid, now); ok {
		return key, nil // fresh, or still cached because the fetch failed
	}
	if err != nil {
		return jose.JSONWebKey{}, err
	}
	return jose.JSONWebKey{}, fmt.Errorf("unknown key id %q", kid)
}

// fetchTimeout bounds a JWKS fetch independently of the request that triggered it.
const fetchTimeout = 5 * time.Second

// usable returns the cached key for kid unless the cache is older than JWKSMaxStale.
func (k *keySet) usable(kid string, now time.Time) (jose.JSONWebKey, bool) {
	if k.fetchedAt.IsZero() || now.Sub(k.fetchedAt) > k.cfg.JWKSMaxStale {
		return jose.JSONWebKey{}, false
	}
	return k.find(kid)
}

func (k *keySet) find(kid string) (jose.JSONWebKey, bool) {
	for _, key := range k.keys {
		if key.KeyID == kid {
			return key, true
		}
	}
	return jose.JSONWebKey{}, false
}

func (k *keySet) fetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.cfg.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := k.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	return set.Keys, nil
}
