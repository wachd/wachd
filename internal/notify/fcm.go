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
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// FCMNotifier sends push notifications to Android devices via the FCM HTTP v1 API.
// Authentication uses a Google service account JWT exchanged for an OAuth2 access token.
//
// Required env vars:
//
//	FCM_SERVICE_ACCOUNT_JSON — full contents of the Firebase service account JSON key file
//	FCM_PROJECT_ID           — Firebase project ID (also present in the JSON, but explicit here)
type FCMNotifier struct {
	projectID   string
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string
	httpClient  *http.Client

	// cached OAuth2 access token — expires after 1 hour; we refresh at 50 minutes
	cachedToken    atomic.Value // stores string
	tokenCreatedAt atomic.Int64 // unix seconds
}

// serviceAccountKey is the subset of fields we need from the Firebase JSON key file.
type serviceAccountKey struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// NewFCMNotifier creates an FCMNotifier from the FCM_SERVICE_ACCOUNT_JSON env var.
// Returns nil if the env var is missing so callers can treat nil as "FCM not configured".
func NewFCMNotifier() *FCMNotifier {
	raw := os.Getenv("FCM_SERVICE_ACCOUNT_JSON")
	if raw == "" {
		return nil
	}

	var sa serviceAccountKey
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return nil
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" || sa.TokenURI == "" {
		return nil
	}

	privateKey, err := parseFCMKey(sa.PrivateKey)
	if err != nil {
		return nil
	}

	return &FCMNotifier{
		projectID:   sa.ProjectID,
		clientEmail: sa.ClientEmail,
		privateKey:  privateKey,
		tokenURI:    sa.TokenURI,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// SendIncidentPush delivers an FCM push notification to each device token.
// Returns the subset of tokens that failed (e.g. unregistered devices).
func (n *FCMNotifier) SendIncidentPush(ctx context.Context, deviceTokens []string, incidentID uuid.UUID, title, body string) []string {
	accessToken, err := n.accessToken(ctx)
	if err != nil {
		return deviceTokens
	}

	var failed []string
	for _, deviceToken := range deviceTokens {
		if err := n.send(ctx, accessToken, deviceToken, incidentID, title, body); err != nil {
			failed = append(failed, deviceToken)
		}
	}
	return failed
}

func (n *FCMNotifier) send(ctx context.Context, accessToken, deviceToken string, incidentID uuid.UUID, title, body string) error {
	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"token": deviceToken,
			"notification": map[string]string{
				"title": title,
				"body":  body,
			},
			"data": map[string]string{
				"incident_id": incidentID.String(),
			},
			"android": map[string]interface{}{
				"notification": map[string]string{
					"channel_id": "incidents",
					"sound":      "default",
				},
				"priority": "high",
			},
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	fcmURL := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", n.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fcmURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var fcmErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&fcmErr)
		return fmt.Errorf("FCM %d: %s", resp.StatusCode, fcmErr.Error.Message)
	}
	return nil
}

// accessToken returns a valid OAuth2 access token, refreshing when > 50 minutes old.
func (n *FCMNotifier) accessToken(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	created := n.tokenCreatedAt.Load()
	if cached, ok := n.cachedToken.Load().(string); ok && cached != "" && now-created < 50*60 {
		return cached, nil
	}

	// Build a JWT assertion to exchange for an access token.
	// https://developers.google.com/identity/protocols/oauth2/service-account
	b64 := base64.RawURLEncoding
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"iss":   n.clientEmail,
		"sub":   n.clientEmail,
		"aud":   n.tokenURI,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"iat":   now,
		"exp":   now + 3600,
	})

	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, n.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign FCM JWT: %w", err)
	}
	jwtAssertion := signingInput + "." + b64.EncodeToString(sig)

	// Exchange the JWT for an access token.
	formData := url.Values{
		"grant_type": {"urn:ietf:params:oauth2:grant-type:jwt-bearer"},
		"assertion":  {jwtAssertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.tokenURI,
		strings.NewReader(formData.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("FCM token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("FCM token exchange %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode FCM token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("FCM token exchange returned empty access_token")
	}

	n.cachedToken.Store(tokenResp.AccessToken)
	n.tokenCreatedAt.Store(now)
	return tokenResp.AccessToken, nil
}

func parseFCMKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from FCM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse FCM private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("FCM key must be an RSA key")
	}
	return rsaKey, nil
}
