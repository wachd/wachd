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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// testKeypair generates a fresh Ed25519 keypair for each test run.
// The private key never leaves this file; no production key is needed.
func testKeypair(t *testing.T) (pubHex string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test keypair: %v", err)
	}
	return hex.EncodeToString(pub), priv
}

// signJWT builds and signs a minimal license JWT with the given claims.
func signJWT(t *testing.T, priv ed25519.PrivateKey, claims jwtClaims) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	msg := header + "." + payloadB64
	sig := ed25519.Sign(priv, []byte(msg))
	return msg + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// validClaims returns a well-formed SMB claim expiring 1 year from now.
func validClaims() jwtClaims {
	return jwtClaims{
		Issuer:       "wachd-license",
		Subject:      "cust_test_001",
		ExpiresAt:    time.Now().Add(365 * 24 * time.Hour).Unix(),
		Tier:         "smb",
		MaxTeams:     10,
		MaxUsers:     50,
		MaxAlerts:    100_000,
		CustomerName: "Acme Corp",
	}
}

// ── OSS defaults ──────────────────────────────────────────────────────────────

func TestOSS_Defaults(t *testing.T) {
	lic := OSS()

	if lic.Tier != TierOpenSource {
		t.Errorf("tier: got %q, want %q", lic.Tier, TierOpenSource)
	}
	if lic.MaxTeams != OSSMaxTeams {
		t.Errorf("MaxTeams: got %d, want %d", lic.MaxTeams, OSSMaxTeams)
	}
	if lic.MaxUsers != OSSMaxUsers {
		t.Errorf("MaxUsers: got %d, want %d", lic.MaxUsers, OSSMaxUsers)
	}
	if lic.MaxAlertsMonth != OSSMaxAlertsMonth {
		t.Errorf("MaxAlertsMonth: got %d, want %d", lic.MaxAlertsMonth, OSSMaxAlertsMonth)
	}
	if lic.IsPaid() {
		t.Error("IsPaid() should be false for OSS")
	}
	if lic.IsEnterprise() {
		t.Error("IsEnterprise() should be false for OSS")
	}
}

func TestOSS_HardcodedLimits(t *testing.T) {
	// Regression guard: these constants must never change without a deliberate decision.
	if OSSMaxTeams != 1 {
		t.Errorf("OSSMaxTeams regression: got %d, want 1", OSSMaxTeams)
	}
	if OSSMaxUsers != 5 {
		t.Errorf("OSSMaxUsers regression: got %d, want 5", OSSMaxUsers)
	}
	if OSSMaxAlertsMonth != 1_000 {
		t.Errorf("OSSMaxAlertsMonth regression: got %d, want 1000", OSSMaxAlertsMonth)
	}
}

// ── Load: empty / whitespace key ─────────────────────────────────────────────

func TestLoad_EmptyKey_ReturnsOSS(t *testing.T) {
	for _, key := range []string{"", "   ", "\t"} {
		lic, err := Load(key)
		if err != nil {
			t.Errorf("key %q: unexpected error: %v", key, err)
		}
		if lic.Tier != TierOpenSource {
			t.Errorf("key %q: got tier %q, want opensource", key, lic.Tier)
		}
	}
}

// ── Load: malformed tokens ────────────────────────────────────────────────────

func TestLoad_MalformedKey_ReturnsOSSWithError(t *testing.T) {
	cases := []string{
		"notavalidtoken",
		"only.two",
		"too.many.dots.here.now",
		"header.!!!invalid_base64!!!.sig",
	}
	for _, key := range cases {
		lic, err := Load(key)
		if err == nil {
			t.Errorf("key %q: expected error, got nil", key)
		}
		if lic.Tier != TierOpenSource {
			t.Errorf("key %q: expected OSS fallback, got %q", key, lic.Tier)
		}
	}
}

// ── Load: valid SMB key ───────────────────────────────────────────────────────

func TestLoad_ValidSMBKey(t *testing.T) {
	pubHex, priv := testKeypair(t)
	token := signJWT(t, priv, validClaims())

	lic, err := LoadWithKey(token, pubHex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lic.Tier != TierSMB {
		t.Errorf("tier: got %q, want smb", lic.Tier)
	}
	if lic.MaxTeams != 10 {
		t.Errorf("MaxTeams: got %d, want 10", lic.MaxTeams)
	}
	if lic.MaxUsers != 50 {
		t.Errorf("MaxUsers: got %d, want 50", lic.MaxUsers)
	}
	if lic.MaxAlertsMonth != 100_000 {
		t.Errorf("MaxAlertsMonth: got %d, want 100000", lic.MaxAlertsMonth)
	}
	if lic.CustomerName != "Acme Corp" {
		t.Errorf("CustomerName: got %q, want Acme Corp", lic.CustomerName)
	}
	if !lic.IsPaid() {
		t.Error("IsPaid() should be true for SMB")
	}
	if lic.IsGracePeriod {
		t.Error("IsGracePeriod should be false for a valid key")
	}
}

// ── Load: valid Enterprise key ────────────────────────────────────────────────

func TestLoad_ValidEnterpriseKey(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.Tier = "enterprise"
	c.MaxTeams = 999
	c.MaxUsers = 9999
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lic.Tier != TierEnterprise {
		t.Errorf("tier: got %q, want enterprise", lic.Tier)
	}
	if !lic.IsEnterprise() {
		t.Error("IsEnterprise() should be true")
	}
	if !lic.IsPaid() {
		t.Error("IsPaid() should be true for Enterprise")
	}
}

// ── Load: tampered signature ──────────────────────────────────────────────────

func TestLoad_TamperedSignature_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	token := signJWT(t, priv, validClaims())

	// Flip the last byte of the signature
	parts := splitToken(token)
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sig[len(sig)-1] ^= 0xFF
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	tampered := parts[0] + "." + parts[1] + "." + parts[2]

	lic, err := LoadWithKey(tampered, pubHex)
	if err == nil {
		t.Error("expected error for tampered signature, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── Load: wrong public key ────────────────────────────────────────────────────

func TestLoad_WrongPublicKey_ReturnsOSSWithError(t *testing.T) {
	_, priv := testKeypair(t)
	wrongPubHex, _ := testKeypair(t) // different keypair — public key won't match
	token := signJWT(t, priv, validClaims())

	lic, err := LoadWithKey(token, wrongPubHex)
	if err == nil {
		t.Error("expected error for wrong public key, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── Load: tampered payload ────────────────────────────────────────────────────

func TestLoad_TamperedPayload_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	token := signJWT(t, priv, validClaims())

	// Replace payload with a different (unsigned) one that claims Enterprise
	evilClaims := validClaims()
	evilClaims.Tier = "enterprise"
	evilClaims.MaxTeams = 9999
	evilPayload, _ := json.Marshal(evilClaims)
	parts := splitToken(token)
	parts[1] = base64.RawURLEncoding.EncodeToString(evilPayload)
	tampered := parts[0] + "." + parts[1] + "." + parts[2]

	lic, err := LoadWithKey(tampered, pubHex)
	if err == nil {
		t.Error("expected error for tampered payload, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── Load: expiry and grace period ────────────────────────────────────────────

func TestLoad_ExpiredKey_BeyondGrace_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.ExpiresAt = time.Now().Add(-8 * 24 * time.Hour).Unix() // 8 days ago — past grace
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err == nil {
		t.Error("expected error for expired key beyond grace, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

func TestLoad_ExpiredKey_WithinGrace_ReturnsPaidWithFlag(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.ExpiresAt = time.Now().Add(-3 * 24 * time.Hour).Unix() // 3 days ago — within 7-day grace
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lic.Tier != TierSMB {
		t.Errorf("tier: got %q, want smb (grace period)", lic.Tier)
	}
	if !lic.IsGracePeriod {
		t.Error("IsGracePeriod should be true within grace window")
	}
}

// ── Load: invalid issuer ──────────────────────────────────────────────────────

func TestLoad_InvalidIssuer_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.Issuer = "not-wachd"
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err == nil {
		t.Error("expected error for invalid issuer, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── Load: unknown tier ────────────────────────────────────────────────────────

func TestLoad_UnknownTier_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.Tier = "platinum"
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err == nil {
		t.Error("expected error for unknown tier, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── Load: zero limits ─────────────────────────────────────────────────────────

func TestLoad_ZeroLimits_ReturnsOSSWithError(t *testing.T) {
	pubHex, priv := testKeypair(t)
	c := validClaims()
	c.MaxTeams = 0
	token := signJWT(t, priv, c)

	lic, err := LoadWithKey(token, pubHex)
	if err == nil {
		t.Error("expected error for zero MaxTeams, got nil")
	}
	if lic.Tier != TierOpenSource {
		t.Errorf("expected OSS fallback, got %q", lic.Tier)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func splitToken(token string) [3]string {
	var parts [3]string
	i := 0
	start := 0
	for j := 0; j < len(token); j++ {
		if token[j] == '.' {
			parts[i] = token[start:j]
			i++
			start = j + 1
		}
	}
	parts[i] = token[start:]
	return parts
}
