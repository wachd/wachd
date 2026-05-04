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

// Package license loads and validates Wachd license keys and enforces tier limits.
//
// Three tiers exist:
//
//	opensource  — no key required; hardcoded limits (1 team, 5 users, 1 000 alerts/month)
//	smb         — signed JWT key; limits embedded in token payload
//	enterprise  — signed JWT key; limits embedded in token payload
//
// The token is a compact JWT (header.payload.signature) signed with Ed25519.
// The private key lives in the private wachd-licensing repository and is never
// committed here. Only the public key is embedded in the binary.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Tier identifies the license tier.
type Tier string

const (
	TierOpenSource Tier = "opensource"
	TierSMB        Tier = "smb"
	TierEnterprise Tier = "enterprise"
)

// OSS hardcoded limits — cannot be raised without a valid paid license key.
// These are constants so the compiler can inline them; a fork that removes the
// limit checks is a license violation, not a technical bypass.
const (
	OSSMaxTeams       = 1
	OSSMaxUsers       = 5
	OSSMaxAlertsMonth = 1_000
)

// gracePeriod is how long an expired key continues to work before the binary
// drops back to OSS limits. Gives customers time to renew without an outage.
const gracePeriod = 7 * 24 * time.Hour

// publicKeys maps key IDs (kid) to their hex-encoded Ed25519 public keys.
// To rotate: generate a new keypair in wachd.ee, add it here as "v2", keep
// "v1" until all v1-signed licenses have expired, then remove "v1".
//
// Corresponding private keys: stored in wachd.ee (private repo).
var publicKeys = map[string]string{
	"v1": "472c48133edeaae3b87b321a096dbaa3e0bd5713833c40331bc559f2d3594a6f",
}

// License holds the decoded, verified limits for this deployment.
// All callers should use the accessor methods rather than reading fields directly.
type License struct {
	Tier           Tier
	MaxTeams       int
	MaxUsers       int
	MaxAlertsMonth int
	CustomerName   string
	JTI            string // unique license ID — used for revocation checks
	ExpiresAt      time.Time
	IsGracePeriod  bool
}

// OSS returns the open-source tier license with hardcoded limits.
// This is always returned when no key is configured or when a key is invalid.
func OSS() *License {
	return &License{
		Tier:           TierOpenSource,
		MaxTeams:       OSSMaxTeams,
		MaxUsers:       OSSMaxUsers,
		MaxAlertsMonth: OSSMaxAlertsMonth,
	}
}

// Load parses and verifies a license key string using the embedded public keys.
//
//   - Empty string  → returns OSS(), nil (no key configured is valid)
//   - Unknown kid   → returns OSS(), non-nil error
//   - Invalid key   → returns OSS(), non-nil error (caller should log a warning)
//   - Expired key within grace period → returns paid license, IsGracePeriod=true
//   - Expired key beyond grace period → returns OSS(), non-nil error
func Load(keyStr string) (*License, error) {
	return loadWithKeys(keyStr, publicKeys)
}

// LoadWithKey is like Load but uses a single public key hex instead of the
// embedded production keys. Only intended for use in tests.
func LoadWithKey(keyStr, publicKeyHex string) (*License, error) {
	return loadWithKeys(keyStr, map[string]string{"v1": publicKeyHex})
}

func loadWithKeys(keyStr string, keys map[string]string) (*License, error) {
	if strings.TrimSpace(keyStr) == "" {
		return OSS(), nil
	}

	c, err := verifyAndDecode(keyStr, keys)
	if err != nil {
		return OSS(), err
	}

	exp := time.Unix(c.ExpiresAt, 0)
	now := time.Now()
	grace := false

	if now.After(exp) {
		if now.After(exp.Add(gracePeriod)) {
			return OSS(), fmt.Errorf("license expired on %s (grace period ended — renew at wachd.io)",
				exp.Format("2006-01-02"))
		}
		grace = true
	}

	tier := Tier(c.Tier)
	if tier != TierSMB && tier != TierEnterprise {
		return OSS(), fmt.Errorf("unknown license tier %q", c.Tier)
	}
	if c.MaxTeams <= 0 || c.MaxUsers <= 0 || c.MaxAlerts <= 0 {
		return OSS(), errors.New("license payload contains zero or negative limits")
	}
	if c.JTI == "" {
		return OSS(), errors.New("license payload missing jti claim")
	}

	return &License{
		Tier:           tier,
		MaxTeams:       c.MaxTeams,
		MaxUsers:       c.MaxUsers,
		MaxAlertsMonth: c.MaxAlerts,
		CustomerName:   c.CustomerName,
		JTI:            c.JTI,
		ExpiresAt:      exp,
		IsGracePeriod:  grace,
	}, nil
}

// IsPaid returns true for SMB and Enterprise tiers.
func (l *License) IsPaid() bool {
	return l.Tier == TierSMB || l.Tier == TierEnterprise
}

// IsEnterprise returns true only for the Enterprise tier.
func (l *License) IsEnterprise() bool {
	return l.Tier == TierEnterprise
}

// ── JWT verification ──────────────────────────────────────────────────────────

// jwtClaims is the expected payload of a Wachd license JWT.
type jwtClaims struct {
	Issuer       string `json:"iss"`
	Subject      string `json:"sub"` // customer ID
	JTI          string `json:"jti"` // unique license ID — revocation handle
	ExpiresAt    int64  `json:"exp"`
	Tier         string `json:"tier"`
	MaxTeams     int    `json:"max_teams"`
	MaxUsers     int    `json:"max_users"`
	MaxAlerts    int    `json:"max_alerts_month"`
	CustomerName string `json:"customer_name"`
}

// jwtHeader is used only to extract the kid field for key lookup.
type jwtHeader struct {
	KID string `json:"kid"`
}

// verifyAndDecode verifies the Ed25519 signature and decodes the JWT payload.
// It selects the public key by reading the kid field from the JWT header.
// It does NOT check expiry — that is the caller's responsibility.
func verifyAndDecode(token string, keys map[string]string) (*jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed license key: expected header.payload.signature")
	}

	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode license header: %w", err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(rawHeader, &hdr); err != nil {
		return nil, fmt.Errorf("parse license header: %w", err)
	}
	if hdr.KID == "" {
		return nil, errors.New("license header missing kid field")
	}

	pubKeyHex, ok := keys[hdr.KID]
	if !ok {
		return nil, fmt.Errorf("unknown key id %q — binary may need upgrading", hdr.KID)
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return nil, errors.New("binary has an invalid embedded public key — please report this")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode license signature: %w", err)
	}

	// Signature covers header.payload exactly as they appear in the token.
	msg := parts[0] + "." + parts[1]
	if !ed25519.Verify(pubKeyBytes, []byte(msg), sig) {
		return nil, errors.New("license signature verification failed — key may be tampered or forged")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode license payload: %w", err)
	}

	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("decode license claims: %w", err)
	}
	if c.Issuer != "wachd-license" {
		return nil, fmt.Errorf("invalid license issuer %q", c.Issuer)
	}

	return &c, nil
}
