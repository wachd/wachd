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
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── APNs notifier tests ───────────────────────────────────────────────────────

func TestNewAPNsNotifier_NilOnMissingEnv(t *testing.T) {
	// All env vars absent — should return nil without panicking.
	t.Setenv("APNS_KEY_ID", "")
	t.Setenv("APNS_TEAM_ID", "")
	t.Setenv("APNS_BUNDLE_ID", "")
	t.Setenv("APNS_PRIVATE_KEY", "")

	if n := NewAPNsNotifier(); n != nil {
		t.Error("expected nil when env vars are missing")
	}
}

func TestAPNsNotifier_TokenStructure(t *testing.T) {
	keyPEM := generateTestECKeyPEM(t)

	t.Setenv("APNS_KEY_ID", "TESTKEY0001")
	t.Setenv("APNS_TEAM_ID", "TESTTEAM001")
	t.Setenv("APNS_BUNDLE_ID", "io.wachd.test")
	t.Setenv("APNS_PRIVATE_KEY", keyPEM)
	t.Setenv("APNS_PRODUCTION", "")

	n := NewAPNsNotifier()
	if n == nil {
		t.Fatal("expected non-nil notifier with valid key")
	}

	jwtToken, err := n.token()
	if err != nil {
		t.Fatalf("token(): %v", err)
	}

	parts := strings.Split(jwtToken, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT must have 3 parts, got %d", len(parts))
	}

	b64 := base64.RawURLEncoding

	// Verify header
	headerJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["alg"] != "ES256" {
		t.Errorf("alg: want ES256, got %s", header["alg"])
	}
	if header["kid"] != "TESTKEY0001" {
		t.Errorf("kid: want TESTKEY0001, got %s", header["kid"])
	}

	// Verify payload
	payloadJSON, err := b64.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["iss"] != "TESTTEAM001" {
		t.Errorf("iss: want TESTTEAM001, got %v", payload["iss"])
	}
	if _, ok := payload["iat"]; !ok {
		t.Error("iat claim missing from JWT payload")
	}

	// Verify signature length: ES256 is 64 bytes (r||s, 32 bytes each)
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) != 64 {
		t.Errorf("ES256 signature must be 64 bytes, got %d", len(sig))
	}
}

func TestAPNsNotifier_TokenCached(t *testing.T) {
	keyPEM := generateTestECKeyPEM(t)

	t.Setenv("APNS_KEY_ID", "TESTKEY0001")
	t.Setenv("APNS_TEAM_ID", "TESTTEAM001")
	t.Setenv("APNS_BUNDLE_ID", "io.wachd.test")
	t.Setenv("APNS_PRIVATE_KEY", keyPEM)

	n := NewAPNsNotifier()
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}

	tok1, err := n.token()
	if err != nil {
		t.Fatalf("first token(): %v", err)
	}
	tok2, err := n.token()
	if err != nil {
		t.Fatalf("second token(): %v", err)
	}
	if tok1 != tok2 {
		t.Error("expected cached token to be returned on second call")
	}
}

func TestAPNsNotifier_SendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("apns-topic") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Verify payload contains incident_id
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["incident_id"]; !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	keyPEM := generateTestECKeyPEM(t)
	n := &APNsNotifier{
		keyID:      "TESTKEY0001",
		teamID:     "TESTTEAM001",
		bundleID:   "io.wachd.test",
		privateKey: parseTestECKey(t, keyPEM),
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	failed := n.SendIncidentPush(
		context.Background(),
		[]string{"device-token-abc"},
		uuid.New(),
		"New Incident",
		"[critical] DB disk 92% full",
	)
	if len(failed) != 0 {
		t.Errorf("expected 0 failures, got %d", len(failed))
	}
}

func TestAPNsNotifier_SendReturnsFailedTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate APNs rejecting the token (e.g. BadDeviceToken)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"reason": "BadDeviceToken"})
	}))
	defer srv.Close()

	keyPEM := generateTestECKeyPEM(t)
	n := &APNsNotifier{
		keyID:      "TESTKEY0001",
		teamID:     "TESTTEAM001",
		bundleID:   "io.wachd.test",
		privateKey: parseTestECKey(t, keyPEM),
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	tokens := []string{"bad-token-1", "bad-token-2"}
	failed := n.SendIncidentPush(context.Background(), tokens, uuid.New(), "title", "body")
	if len(failed) != 2 {
		t.Errorf("expected both tokens in failed list, got %d", len(failed))
	}
}

// ── FCM notifier tests ────────────────────────────────────────────────────────

func TestNewFCMNotifier_NilOnMissingEnv(t *testing.T) {
	t.Setenv("FCM_SERVICE_ACCOUNT_JSON", "")

	if n := NewFCMNotifier(); n != nil {
		t.Error("expected nil when env var is missing")
	}
}

func TestNewFCMNotifier_NilOnMalformedJSON(t *testing.T) {
	t.Setenv("FCM_SERVICE_ACCOUNT_JSON", "not-valid-json")

	if n := NewFCMNotifier(); n != nil {
		t.Error("expected nil on malformed JSON")
	}
}

func TestNewFCMNotifier_NilOnMissingFields(t *testing.T) {
	// JSON parses but required fields are empty
	t.Setenv("FCM_SERVICE_ACCOUNT_JSON", `{"project_id":"","client_email":"","private_key":"","token_uri":""}`)

	if n := NewFCMNotifier(); n != nil {
		t.Error("expected nil when JSON fields are empty")
	}
}

func TestFCMNotifier_SendSuccess(t *testing.T) {
	// Token exchange endpoint
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.FormValue("grant_type") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
		})
	}))
	defer tokenSrv.Close()

	// FCM messages endpoint — intercepts via transport
	var capturedBody bytes.Buffer
	fcmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = capturedBody.ReadFrom(r.Body)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "projects/test/messages/1"})
	}))
	defer fcmSrv.Close()

	rsaKeyPEM := generateTestRSAKeyPEM(t)
	n := &FCMNotifier{
		projectID:   "test-project",
		clientEmail: "test@test.iam.gserviceaccount.com",
		privateKey:  parseTestRSAKey(t, rsaKeyPEM),
		tokenURI:    tokenSrv.URL,
		httpClient:  &http.Client{Transport: &fcmTestTransport{tokenSrv: tokenSrv, fcmSrv: fcmSrv}},
	}

	incidentID := uuid.New()
	failed := n.SendIncidentPush(
		context.Background(),
		[]string{"android-device-token"},
		incidentID,
		"New Incident",
		"[high] Redis eviction storm",
	)
	if len(failed) != 0 {
		t.Errorf("expected 0 failures, got %d", len(failed))
	}

	// Verify the FCM payload contains the incident_id in data
	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody.Bytes(), &payload); err != nil {
		t.Fatalf("parse captured body: %v", err)
	}
	msg, ok := payload["message"].(map[string]interface{})
	if !ok {
		t.Fatal("payload.message missing or wrong type")
	}
	data, ok := msg["data"].(map[string]interface{})
	if !ok {
		t.Fatal("payload.message.data missing or wrong type")
	}
	if data["incident_id"] != incidentID.String() {
		t.Errorf("incident_id mismatch: want %s, got %v", incidentID.String(), data["incident_id"])
	}
}

func TestFCMNotifier_TokenCached(t *testing.T) {
	calls := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	}))
	defer tokenSrv.Close()

	rsaKeyPEM := generateTestRSAKeyPEM(t)
	n := &FCMNotifier{
		projectID:   "test-project",
		clientEmail: "test@test.iam.gserviceaccount.com",
		privateKey:  parseTestRSAKey(t, rsaKeyPEM),
		tokenURI:    tokenSrv.URL,
		httpClient:  tokenSrv.Client(),
	}

	ctx := context.Background()
	if _, err := n.accessToken(ctx); err != nil {
		t.Fatalf("first accessToken: %v", err)
	}
	if _, err := n.accessToken(ctx); err != nil {
		t.Fatalf("second accessToken: %v", err)
	}
	if calls != 1 {
		t.Errorf("token endpoint should be called once (cached); got %d calls", calls)
	}
}

func TestFCMNotifier_TokenExpiry(t *testing.T) {
	calls := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "tok"})
	}))
	defer tokenSrv.Close()

	rsaKeyPEM := generateTestRSAKeyPEM(t)
	n := &FCMNotifier{
		projectID:   "test-project",
		clientEmail: "test@test.iam.gserviceaccount.com",
		privateKey:  parseTestRSAKey(t, rsaKeyPEM),
		tokenURI:    tokenSrv.URL,
		httpClient:  tokenSrv.Client(),
	}

	ctx := context.Background()
	// Simulate an expired token by backdating tokenCreatedAt by 51 minutes
	n.cachedToken.Store("old-token")
	n.tokenCreatedAt.Store(time.Now().Add(-51 * time.Minute).Unix())

	if _, err := n.accessToken(ctx); err != nil {
		t.Fatalf("accessToken after expiry: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected token refresh after expiry; got %d calls", calls)
	}
}

// ── Test key helpers ──────────────────────────────────────────────────────────

func generateTestECKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return buf.String()
}

func parseTestECKey(t *testing.T, pemData string) *ecdsa.PrivateKey {
	t.Helper()
	key, err := parseAPNsKey(pemData)
	if err != nil {
		t.Fatalf("parseAPNsKey: %v", err)
	}
	return key
}

func generateTestRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal RSA key: %v", err)
	}
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return buf.String()
}

func parseTestRSAKey(t *testing.T, pemData string) *rsa.PrivateKey {
	t.Helper()
	key, err := parseFCMKey(pemData)
	if err != nil {
		t.Fatalf("parseFCMKey: %v", err)
	}
	return key
}

// fcmTestTransport routes token exchange requests to tokenSrv and FCM requests to fcmSrv.
type fcmTestTransport struct {
	tokenSrv *httptest.Server
	fcmSrv   *httptest.Server
}

func (tr *fcmTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, tr.tokenSrv.Listener.Addr().String()) ||
		req.URL.Path == "/token" ||
		strings.HasSuffix(req.URL.Host, tr.tokenSrv.Listener.Addr().String()) {
		req.URL.Host = tr.tokenSrv.Listener.Addr().String()
		req.URL.Scheme = "http"
	} else {
		req.URL.Host = tr.fcmSrv.Listener.Addr().String()
		req.URL.Scheme = "http"
	}
	return http.DefaultTransport.RoundTrip(req)
}
