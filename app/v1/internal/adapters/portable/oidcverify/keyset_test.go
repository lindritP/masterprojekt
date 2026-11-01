package oidcverify

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// jwksServer serves the public half of key, or fails while down is set.
type jwksServer struct {
	*httptest.Server
	fetches atomic.Int32
	down    atomic.Bool
}

func newJWKSServer(t *testing.T, keys ...*rsa.PrivateKey) *jwksServer {
	t.Helper()
	s := &jwksServer{}
	var set jose.JSONWebKeySet
	for i, k := range keys {
		set.Keys = append(set.Keys, jose.JSONWebKey{Key: &k.PublicKey, KeyID: string(rune('a' + i)), Algorithm: "RS256", Use: "sig"})
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.fetches.Add(1)
		if s.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(s.Close)
	return s
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	jws, err := signer.Sign([]byte(`{"sub":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// While the issuer is down after the TTL expired, cached keys keep working
// and the issuer is asked at most once per MinRefreshInterval.
func TestKeySet_StaleWhileErrorAndRateLimited(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, key)
	now := time.Unix(1_800_000_000, 0)
	ks := &keySet{cfg: Config{
		JWKSURL: srv.URL, JWKSTTL: time.Minute, JWKSMaxStale: 6 * time.Hour, MinRefreshInterval: 10 * time.Second,
		HTTPClient: srv.Client(), Now: func() time.Time { return now },
	}}
	tok := sign(t, key, "a")
	ctx := context.Background()

	if _, err := ks.VerifySignature(ctx, tok); err != nil {
		t.Fatal(err)
	}
	srv.down.Store(true)
	now = now.Add(2 * time.Minute) // TTL expired

	for range 50 {
		if _, err := ks.VerifySignature(ctx, tok); err != nil {
			t.Fatalf("cached key rejected while the issuer is down: %v", err)
		}
	}
	if got := srv.fetches.Load(); got != 2 {
		t.Errorf("issuer fetched %d times, want 2 (initial + one rate-limited retry)", got)
	}

	now = now.Add(11 * time.Second)
	if _, err := ks.VerifySignature(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if got := srv.fetches.Load(); got != 3 {
		t.Errorf("after the interval: %d fetches, want 3", got)
	}
}

// Cached keys stop being accepted once they are older than JWKSMaxStale, even
// though the issuer is still unreachable (a revoked key must not live forever).
func TestKeySet_MaxStaleFailsClosed(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, key)
	now := time.Unix(1_800_000_000, 0)
	ks := &keySet{cfg: Config{
		JWKSURL: srv.URL, JWKSTTL: time.Minute, JWKSMaxStale: time.Hour, MinRefreshInterval: 10 * time.Second,
		HTTPClient: srv.Client(), Now: func() time.Time { return now },
	}}
	tok := sign(t, key, "a")
	ctx := context.Background()
	if _, err := ks.VerifySignature(ctx, tok); err != nil {
		t.Fatal(err)
	}
	srv.down.Store(true)
	now = now.Add(59 * time.Minute)
	if _, err := ks.VerifySignature(ctx, tok); err != nil {
		t.Errorf("within JWKSMaxStale the cached key must still work: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := ks.VerifySignature(ctx, tok); err == nil {
		t.Error("cached key accepted beyond JWKSMaxStale")
	}
}

// A caller that has already gone away cannot abort the shared fetch.
func TestKeySet_FetchIgnoresCallerCancellation(t *testing.T) {
	key := newKey(t)
	srv := newJWKSServer(t, key)
	ks := &keySet{cfg: Config{
		JWKSURL: srv.URL, JWKSTTL: time.Hour, JWKSMaxStale: 6 * time.Hour, MinRefreshInterval: 10 * time.Second,
		HTTPClient: srv.Client(), Now: time.Now,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ks.VerifySignature(ctx, sign(t, key, "a")); err != nil {
		t.Fatalf("fetch was aborted by the caller's cancellation: %v", err)
	}
}

// Tokens with an unknown key id trigger at most one fetch per interval.
func TestKeySet_UnknownKidIsRateLimited(t *testing.T) {
	known, unknown := newKey(t), newKey(t)
	srv := newJWKSServer(t, known)
	now := time.Unix(1_800_000_000, 0)
	ks := &keySet{cfg: Config{
		JWKSURL: srv.URL, JWKSTTL: time.Hour, JWKSMaxStale: 6 * time.Hour, MinRefreshInterval: 10 * time.Second,
		HTTPClient: srv.Client(), Now: func() time.Time { return now },
	}}
	ctx := context.Background()
	if _, err := ks.VerifySignature(ctx, sign(t, known, "a")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	for range 20 {
		if _, err := ks.VerifySignature(ctx, sign(t, unknown, "z")); err == nil {
			t.Fatal("token with an unknown key id accepted")
		}
	}
	if got := srv.fetches.Load(); got != 2 {
		t.Errorf("issuer fetched %d times, want 2", got)
	}
}
