// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// revocationServer builds an httptest.Server that returns a signed revocation
// list containing the given JTIs. Returns the server, the kid used, and the
// public key hex so callers can build a matching key map.
func revocationServer(t *testing.T, revokedJTIs []string) (*httptest.Server, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate revocation keypair: %v", err)
	}
	kid := "v1"
	pubHex := hex.EncodeToString(pub)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revokedJTIs == nil {
			revokedJTIs = []string{}
		}
		p := revokedPayload{Revoked: revokedJTIs, IssuedAt: time.Now().UTC().Unix()}
		msg, _ := json.Marshal(p)
		sig := ed25519.Sign(priv, msg)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revoked":   revokedJTIs,
			"issued_at": p.IssuedAt,
			"kid":       kid,
			"sig":       base64.RawURLEncoding.EncodeToString(sig),
		})
	}))
	t.Cleanup(ts.Close)
	return ts, kid, pubHex
}

// keys builds a single-entry map for use with CheckRevocationWithConfig.
func keys(kid, pubHex string) map[string]string {
	return map[string]string{kid: pubHex}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestCheckRevocation_Revoked(t *testing.T) {
	const jti = "a1b2c3d4-0000-4000-8000-000000000001"
	ts, kid, pubHex := revocationServer(t, []string{jti, "other-jti"})

	err := CheckRevocationWithConfig(context.Background(), jti, ts.URL, keys(kid, pubHex))
	if !errors.Is(err, ErrRevoked) {
		t.Errorf("got %v, want ErrRevoked", err)
	}
}

func TestCheckRevocation_NotRevoked(t *testing.T) {
	const jti = "a1b2c3d4-0000-4000-8000-000000000001"
	ts, kid, pubHex := revocationServer(t, []string{"some-other-jti"})

	err := CheckRevocationWithConfig(context.Background(), jti, ts.URL, keys(kid, pubHex))
	if err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestCheckRevocation_EmptyList(t *testing.T) {
	const jti = "a1b2c3d4-0000-4000-8000-000000000001"
	ts, kid, pubHex := revocationServer(t, []string{})

	err := CheckRevocationWithConfig(context.Background(), jti, ts.URL, keys(kid, pubHex))
	if err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestCheckRevocation_NilListFromServer(t *testing.T) {
	// Server returns JSON null for revoked — should be treated as empty.
	pub, priv, _ := ed25519.GenerateKey(nil)
	kid := "v1"
	pubHex := hex.EncodeToString(pub)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuedAt := time.Now().UTC().Unix()
		// Sign with nil guard matching the verifier's nil guard
		p := revokedPayload{Revoked: []string{}, IssuedAt: issuedAt}
		msg, _ := json.Marshal(p)
		sig := ed25519.Sign(priv, msg)
		// But return null in the JSON body
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revoked":   nil,
			"issued_at": issuedAt,
			"kid":       kid,
			"sig":       base64.RawURLEncoding.EncodeToString(sig),
		})
	}))
	defer ts.Close()

	err := CheckRevocationWithConfig(context.Background(), "any-jti", ts.URL, keys(kid, pubHex))
	if err != nil {
		t.Errorf("got %v, want nil (null revoked list should be safe)", err)
	}
}

func TestCheckRevocation_EndpointUnreachable(t *testing.T) {
	// Port 1 is never open — connection should fail immediately.
	err := CheckRevocationWithConfig(context.Background(), "any-jti",
		"http://127.0.0.1:1/revoked", map[string]string{"v1": "aabbcc"})
	if err != nil {
		t.Errorf("got %v, want nil (unreachable endpoint must fail open)", err)
	}
}

func TestCheckRevocation_Non200Response(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	err := CheckRevocationWithConfig(context.Background(), "any-jti",
		ts.URL, map[string]string{"v1": "aabbcc"})
	if err != nil {
		t.Errorf("got %v, want nil (non-200 must fail open)", err)
	}
}

func TestCheckRevocation_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer ts.Close()

	err := CheckRevocationWithConfig(context.Background(), "any-jti",
		ts.URL, map[string]string{"v1": "aabbcc"})
	if err != nil {
		t.Errorf("got %v, want nil (malformed JSON must fail open)", err)
	}
}

func TestCheckRevocation_InvalidSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	kid := "v1"
	pubHex := hex.EncodeToString(pub)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jtis := []string{"some-jti"}
		p := revokedPayload{Revoked: jtis, IssuedAt: time.Now().UTC().Unix()}
		msg, _ := json.Marshal(p)
		sig := ed25519.Sign(priv, msg)
		// Corrupt the last byte of the signature
		sig[len(sig)-1] ^= 0xFF
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revoked":   jtis,
			"issued_at": p.IssuedAt,
			"kid":       kid,
			"sig":       base64.RawURLEncoding.EncodeToString(sig),
		})
	}))
	defer ts.Close()

	err := CheckRevocationWithConfig(context.Background(), "some-jti", ts.URL, keys(kid, pubHex))
	if err == nil {
		t.Error("want error for invalid signature, got nil")
	}
	if errors.Is(err, ErrRevoked) {
		t.Error("invalid signature must not produce ErrRevoked")
	}
}

func TestCheckRevocation_UnknownKID(t *testing.T) {
	ts, _, pubHex := revocationServer(t, []string{"some-jti"})
	// Pass a key map with "v2" only — the server returns "v1"
	err := CheckRevocationWithConfig(context.Background(), "some-jti",
		ts.URL, map[string]string{"v2": pubHex})
	if err == nil {
		t.Error("want error for unknown kid, got nil")
	}
	if errors.Is(err, ErrRevoked) {
		t.Error("unknown kid must not produce ErrRevoked")
	}
}

func TestCheckRevocation_WrongPublicKey(t *testing.T) {
	// Build a server signed with key A, but pass key B in the key map.
	ts, kid, _ := revocationServer(t, []string{"target-jti"})
	_, wrongPubHex := func() (ed25519.PublicKey, string) {
		pub, _, _ := ed25519.GenerateKey(nil)
		return pub, hex.EncodeToString(pub)
	}()

	err := CheckRevocationWithConfig(context.Background(), "target-jti",
		ts.URL, keys(kid, wrongPubHex))
	if err == nil {
		t.Error("want error for wrong public key, got nil")
	}
	if errors.Is(err, ErrRevoked) {
		t.Error("wrong public key must not produce ErrRevoked")
	}
}

func TestCheckRevocation_MalformedBase64Sig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revoked":   []string{},
			"issued_at": time.Now().UTC().Unix(),
			"kid":       "v1",
			"sig":       "!!!not_valid_base64!!!",
		})
	}))
	defer ts.Close()

	pub, _, _ := ed25519.GenerateKey(nil)
	err := CheckRevocationWithConfig(context.Background(), "any-jti",
		ts.URL, map[string]string{"v1": hex.EncodeToString(pub)})
	if err == nil {
		t.Error("want error for malformed base64 sig, got nil")
	}
	if errors.Is(err, ErrRevoked) {
		t.Error("malformed sig must not produce ErrRevoked")
	}
}

func TestCheckRevocation_ContextCancelled(t *testing.T) {
	// Slow server — should be cut off by a pre-cancelled context.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := CheckRevocationWithConfig(ctx, "any-jti",
		ts.URL, map[string]string{"v1": "aabbcc"})
	// A cancelled context causes http.Do to fail — must fail open (nil).
	if err != nil {
		t.Errorf("got %v, want nil (cancelled context must fail open)", err)
	}
}
