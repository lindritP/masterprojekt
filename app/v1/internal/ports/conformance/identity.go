package conformance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// VerifierParams is the provider-neutral part of a verifier's configuration.
type VerifierParams struct {
	Issuer   string
	Audience string
	JWKSURL  string
	Now      func() time.Time
}

// IdentityVerifierFixture provides a live identity provider and a way to
// build verifiers against it.
type IdentityVerifierFixture struct {
	Params VerifierParams // the correct configuration
	New    func(p VerifierParams) (ports.IdentityVerifier, error)

	// Token returns a fresh access token for a test client holding WantRole.
	Token    func(ctx context.Context) (string, error)
	WantRole string

	// RotateKey makes the provider sign new tokens with a new key. restore
	// undoes the rotation.
	RotateKey func(ctx context.Context) (restore func(), err error)
}

// TestIdentityVerifier runs the IdentityVerifier semantics of SRS §7.4.
func TestIdentityVerifier(t *testing.T, f IdentityVerifierFixture) {
	ctx := context.Background()
	verifier := func(t *testing.T, change func(*VerifierParams)) ports.IdentityVerifier {
		t.Helper()
		p := f.Params
		if change != nil {
			change(&p)
		}
		v, err := f.New(p)
		mustOK(t, err)
		return v
	}
	token := func(t *testing.T) string {
		t.Helper()
		tok, err := f.Token(ctx)
		mustOK(t, err)
		return tok
	}
	rejects := func(t *testing.T, v ports.IdentityVerifier, tok, why string) {
		t.Helper()
		if _, err := v.Verify(ctx, tok); !errors.Is(err, ports.ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want ErrUnauthenticated", why, err)
		}
	}

	t.Run("Verify_RejectsExpired", func(t *testing.T) {
		v := verifier(t, func(p *VerifierParams) { p.Now = func() time.Time { return time.Now().Add(2 * time.Hour) } })
		rejects(t, v, token(t), "token two hours past issue")
	})

	t.Run("Verify_RejectsWrongAudience", func(t *testing.T) {
		v := verifier(t, func(p *VerifierParams) { p.Audience = "not-" + p.Audience })
		rejects(t, v, token(t), "wrong audience")
	})

	t.Run("Verify_RejectsWrongIssuer", func(t *testing.T) {
		v := verifier(t, func(p *VerifierParams) { p.Issuer = "https://issuer.invalid/realms/other" })
		rejects(t, v, token(t), "wrong issuer")
	})

	t.Run("Verify_RejectsBadSignature", func(t *testing.T) {
		parts := strings.Split(token(t), ".")
		sig := []byte(parts[2])
		mid := len(sig) / 2
		if sig[mid] == 'A' {
			sig[mid] = 'B'
		} else {
			sig[mid] = 'A'
		}
		parts[2] = string(sig)
		rejects(t, verifier(t, nil), strings.Join(parts, "."), "tampered signature")
	})

	t.Run("Verify_MapsClaimsDeterministically", func(t *testing.T) {
		v := verifier(t, nil)
		tok := token(t)
		a, err := v.Verify(ctx, tok)
		mustOK(t, err)
		b, err := v.Verify(ctx, tok)
		mustOK(t, err)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("same token mapped to %+v and %+v", a, b)
		}
		if a.ID == "" || a.ID != claim(t, tok, "sub") {
			t.Errorf("subject id %q, want the token's sub %q", a.ID, claim(t, tok, "sub"))
		}
		if !slices.Contains(a.Roles, f.WantRole) {
			t.Errorf("roles %v do not contain %q", a.Roles, f.WantRole)
		}
	})

	t.Run("Verify_HandlesKeyRotation", func(t *testing.T) {
		v := verifier(t, nil)
		before := token(t)
		_, err := v.Verify(ctx, before) // warms the key cache
		mustOK(t, err)

		restore, err := f.RotateKey(ctx)
		mustOK(t, err)
		defer restore()
		after := token(t)
		if header(t, after, "kid") == header(t, before, "kid") {
			t.Fatal("rotation did not change the signing key")
		}
		if _, err := v.Verify(ctx, after); err != nil {
			t.Errorf("token signed with the rotated key rejected: %v", err)
		}
	})
}

func claim(t *testing.T, tok, name string) string {
	t.Helper()
	return segment(t, tok, 1)[name]
}

func header(t *testing.T, tok, name string) string {
	t.Helper()
	return segment(t, tok, 0)[name]
}

func segment(t *testing.T, tok string, i int) map[string]string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[i])
	mustOK(t, err)
	var all map[string]any
	mustOK(t, json.Unmarshal(raw, &all))
	out := map[string]string{}
	for k, v := range all {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
