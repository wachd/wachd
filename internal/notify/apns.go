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

package notify

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// APNsNotifier sends push notifications to iOS devices via the APNs HTTP/2 API.
// Authentication uses APNs token-based auth (JWT / ES256) — no certificate needed.
//
// Required env vars:
//
//	APNS_KEY_ID       — 10-char key ID from Apple Developer portal
//	APNS_TEAM_ID      — 10-char Apple Team ID
//	APNS_BUNDLE_ID    — app bundle identifier (e.g. io.wachd.app)
//	APNS_PRIVATE_KEY  — PEM-encoded ES256 private key (.p8 file contents)
//	APNS_PRODUCTION   — "true" for production APNs gateway; default is sandbox
type APNsNotifier struct {
	keyID      string
	teamID     string
	bundleID   string
	privateKey *ecdsa.PrivateKey
	baseURL    string
	httpClient *http.Client

	// JWT cache — APNs tokens are valid for 60m; we regenerate every 45m.
	cachedToken    atomic.Value // stores string
	tokenCreatedAt atomic.Int64 // unix seconds
}

// NewAPNsNotifier creates an APNsNotifier from environment variables.
// Returns nil if any required variable is missing so callers can treat nil
// as "APNs not configured" without extra nil checks on every send.
func NewAPNsNotifier() *APNsNotifier {
	keyID := os.Getenv("APNS_KEY_ID")
	teamID := os.Getenv("APNS_TEAM_ID")
	bundleID := os.Getenv("APNS_BUNDLE_ID")
	rawKey := os.Getenv("APNS_PRIVATE_KEY")
	if keyID == "" || teamID == "" || bundleID == "" || rawKey == "" {
		return nil
	}

	privateKey, err := parseAPNsKey(rawKey)
	if err != nil {
		return nil
	}

	baseURL := "https://api.sandbox.push.apple.com"
	if os.Getenv("APNS_PRODUCTION") == "true" {
		baseURL = "https://api.push.apple.com"
	}

	return &APNsNotifier{
		keyID:      keyID,
		teamID:     teamID,
		bundleID:   bundleID,
		privateKey: privateKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SendIncidentPush delivers an alert push to each device token in the list.
// Returns the subset of tokens that failed (caller can log / prune stale tokens).
func (n *APNsNotifier) SendIncidentPush(ctx context.Context, deviceTokens []string, incidentID uuid.UUID, title, body string) []string {
	jwtToken, err := n.token()
	if err != nil {
		return deviceTokens // all failed — can't sign
	}

	payload := map[string]interface{}{
		"aps": map[string]interface{}{
			"alert": map[string]string{
				"title": title,
				"body":  body,
			},
			"sound":    "default",
			"badge":    1,
			"category": "INCIDENT",
		},
		"incident_id": incidentID.String(),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return deviceTokens
	}

	var failed []string
	for _, deviceToken := range deviceTokens {
		if err := n.send(ctx, jwtToken, deviceToken, payloadBytes); err != nil {
			failed = append(failed, deviceToken)
		}
	}
	return failed
}

func (n *APNsNotifier) send(ctx context.Context, jwtToken, deviceToken string, payload []byte) error {
	url := fmt.Sprintf("%s/3/device/%s", n.baseURL, deviceToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+jwtToken)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-topic", n.bundleID)
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(30*time.Minute).Unix(), 10))
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apnsErr struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apnsErr)
		return fmt.Errorf("APNs %d: %s", resp.StatusCode, apnsErr.Reason)
	}
	return nil
}

// token returns a valid APNs JWT, regenerating when the cached one is > 45 minutes old.
// Implements ES256 signing without an external JWT library.
func (n *APNsNotifier) token() (string, error) {
	now := time.Now().Unix()
	created := n.tokenCreatedAt.Load()
	if cached, ok := n.cachedToken.Load().(string); ok && cached != "" && now-created < 45*60 {
		return cached, nil
	}

	// Build header and payload, base64url-encode each.
	headerJSON, err := json.Marshal(map[string]string{"alg": "ES256", "kid": n.keyID})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(map[string]interface{}{"iss": n.teamID, "iat": now})
	if err != nil {
		return "", err
	}

	b64 := base64.RawURLEncoding
	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)

	// ES256 = ECDSA with SHA-256.
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, n.privateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign APNs JWT: %w", err)
	}

	// JWT ES256 signature format: fixed-size r||s (32 bytes each for P-256).
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)

	signed := signingInput + "." + b64.EncodeToString(sig)
	n.cachedToken.Store(signed)
	n.tokenCreatedAt.Store(now)
	return signed, nil
}

func parseAPNsKey(pemData string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from APNS_PRIVATE_KEY")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse APNs private key: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("APNs key must be an EC key")
	}
	return ecKey, nil
}
