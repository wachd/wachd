package notify

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- test helpers ---

// generateTestPEM returns a fresh Ed25519 public key and its PKCS8 PEM private key.
func generateTestPEM(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 keypair: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pub, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// newTestNotifier builds a RelayNotifier directly (bypasses env vars) for behavioral tests.
func newTestNotifier(t *testing.T, serverURL string) (*RelayNotifier, ed25519.PublicKey) {
	t.Helper()
	pub, pemData := generateTestPEM(t)
	priv, err := parseRelayKey(pemData)
	if err != nil {
		t.Fatalf("parseRelayKey: %v", err)
	}
	return &RelayNotifier{
		relayURL:     serverURL,
		deploymentID: "test-deploy-id",
		privateKey:   priv,
		httpClient:   &http.Client{},
	}, pub
}

// checkSignature verifies the Ed25519 signature using the exact message format.
// Safe to call from an httptest handler goroutine — uses t.Errorf (not t.Fatal).
// Returns false if verification fails.
func checkSignature(t *testing.T, r *http.Request, body []byte, pub ed25519.PublicKey) bool {
	t.Helper()
	timestampStr := r.Header.Get("X-Wachd-Timestamp")
	nonce := r.Header.Get("X-Wachd-Nonce")
	sigB64 := r.Header.Get("X-Wachd-Signature")

	bodyHash := sha256.Sum256(body)
	expectedMsg := "send:" + timestampStr + ":" + nonce + ":" + hex.EncodeToString(bodyHash[:])

	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Errorf("decode X-Wachd-Signature: %v", err)
		return false
	}
	if !ed25519.Verify(pub, []byte(expectedMsg), sig) {
		t.Errorf("Ed25519 signature invalid; signed message was: %q", expectedMsg)
		return false
	}
	return true
}

// okResponse writes a standard relay success response.
func okResponse(w http.ResponseWriter, failedTokens []string) {
	if failedTokens == nil {
		failedTokens = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"failed_tokens": failedTokens}})
}

// --- NewRelayNotifier configuration tests ---

func TestNewRelayNotifier_NotConfigured(t *testing.T) {
	t.Setenv("WACHD_PUSH_RELAY_URL", "")
	t.Setenv("WACHD_PUSH_RELAY_DEPLOYMENT_ID", "")
	t.Setenv("WACHD_PUSH_RELAY_PRIVATE_KEY", "")

	n, err := NewRelayNotifier()
	if err != nil {
		t.Fatalf("expected nil error when no vars set, got: %v", err)
	}
	if n != nil {
		t.Fatal("expected nil notifier when no vars set")
	}
}

func TestNewRelayNotifier_PartialConfig(t *testing.T) {
	_, pemData := generateTestPEM(t)
	cases := []struct {
		name string
		url  string
		id   string
		key  string
	}{
		{"url only", "https://push.wachd.io", "", ""},
		{"id only", "", "some-id", ""},
		{"key only", "", "", pemData},
		{"url and id", "https://push.wachd.io", "some-id", ""},
		{"url and key", "https://push.wachd.io", "", pemData},
		{"id and key", "", "some-id", pemData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WACHD_PUSH_RELAY_URL", tc.url)
			t.Setenv("WACHD_PUSH_RELAY_DEPLOYMENT_ID", tc.id)
			t.Setenv("WACHD_PUSH_RELAY_PRIVATE_KEY", tc.key)

			n, err := NewRelayNotifier()
			if err == nil {
				t.Fatal("expected error for partial relay config, got nil")
			}
			if n != nil {
				t.Fatal("expected nil notifier for partial relay config")
			}
		})
	}
}

func TestNewRelayNotifier_InvalidKey(t *testing.T) {
	t.Setenv("WACHD_PUSH_RELAY_URL", "https://push.wachd.io")
	t.Setenv("WACHD_PUSH_RELAY_DEPLOYMENT_ID", "some-id")
	t.Setenv("WACHD_PUSH_RELAY_PRIVATE_KEY", "not-valid-pem")

	n, err := NewRelayNotifier()
	if err == nil {
		t.Fatal("expected error for invalid private key, got nil")
	}
	if n != nil {
		t.Fatal("expected nil notifier for invalid private key")
	}
}

func TestNewRelayNotifier_Valid(t *testing.T) {
	_, pemData := generateTestPEM(t)
	t.Setenv("WACHD_PUSH_RELAY_URL", "https://push.wachd.io")
	t.Setenv("WACHD_PUSH_RELAY_DEPLOYMENT_ID", "some-deployment-id")
	t.Setenv("WACHD_PUSH_RELAY_PRIVATE_KEY", pemData)

	n, err := NewRelayNotifier()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n == nil {
		t.Fatal("expected notifier, got nil")
	}
}

// --- PEM key parsing tests ---

func TestParseRelayKey_RealNewlines(t *testing.T) {
	_, pemData := generateTestPEM(t)
	// pemData already has real newlines — standard PEM format
	if _, err := parseRelayKey(pemData); err != nil {
		t.Fatalf("parseRelayKey with real newlines: %v", err)
	}
}

func TestParseRelayKey_EscapedNewlines(t *testing.T) {
	_, pemData := generateTestPEM(t)
	// Simulate how k8s Secrets sometimes encode PEM in env vars: literal \n instead of newline
	escaped := strings.ReplaceAll(pemData, "\n", `\n`)
	if _, err := parseRelayKey(escaped); err != nil {
		t.Fatalf("parseRelayKey with escaped \\n: %v", err)
	}
}

func TestParseRelayKey_InvalidPEM(t *testing.T) {
	if _, err := parseRelayKey("not a pem block"); err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
}

// --- Request format: signing, payload isolation, headers ---

// TestSendPush_RequestFormat verifies all three invariants of a relay request in one
// round-trip: correct Ed25519 signature over the exact message format, payload
// containing only the three permitted fields, and all required auth headers present.
func TestSendPush_RequestFormat(t *testing.T) {
	var pub ed25519.PublicKey // set before handler executes

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}

		// Required auth headers
		for _, h := range []string{"X-Wachd-Deployment-ID", "X-Wachd-Timestamp", "X-Wachd-Nonce", "X-Wachd-Signature"} {
			if r.Header.Get(h) == "" {
				t.Errorf("missing required header: %s", h)
				http.Error(w, "missing header "+h, http.StatusBadRequest)
				return
			}
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", r.Header.Get("Content-Type"))
			http.Error(w, "wrong content-type", http.StatusBadRequest)
			return
		}

		// Ed25519 signature: exact message format send:<timestamp>:<nonce>:<hex(sha256(body))>
		if !checkSignature(t, r, body, pub) {
			http.Error(w, "signature invalid", http.StatusUnauthorized)
			return
		}

		// Payload must contain exactly device_tokens, incident_id, platform — nothing else
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("unmarshal payload: %v", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		for _, required := range []string{"device_tokens", "incident_id", "platform"} {
			if _, ok := payload[required]; !ok {
				t.Errorf("payload missing required field %q", required)
			}
		}
		for k := range payload {
			switch k {
			case "device_tokens", "incident_id", "platform":
			default:
				t.Errorf("payload contains unexpected field %q — incident content must not leave the cluster", k)
			}
		}

		okResponse(w, nil)
	}))
	defer srv.Close()

	var n *RelayNotifier
	n, pub = newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", []string{"tok1", "tok2"}, "incident-uuid-123")
	if len(failed) != 0 {
		t.Fatalf("expected no failed tokens, got: %v", failed)
	}
}

// --- Response handling tests ---

func TestSendPush_AllSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		okResponse(w, nil)
	}))
	defer srv.Close()

	n, _ := newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", []string{"tok1", "tok2", "tok3"}, "inc-1")
	if len(failed) != 0 {
		t.Fatalf("expected zero failures, got: %v", failed)
	}
}

func TestSendPush_PartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		okResponse(w, []string{"tok2"})
	}))
	defer srv.Close()

	n, _ := newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", []string{"tok1", "tok2", "tok3"}, "inc-1")
	if len(failed) != 1 || failed[0] != "tok2" {
		t.Fatalf("expected [tok2] failed, got: %v", failed)
	}
}

func TestSendPush_Empty200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK) // empty body — cannot be decoded
	}))
	defer srv.Close()

	tokens := []string{"tok1", "tok2"}
	n, _ := newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", tokens, "inc-1")
	if len(failed) != len(tokens) {
		t.Fatalf("expected all %d tokens failed on empty 200, got %d: %v", len(tokens), len(failed), failed)
	}
}

func TestSendPush_Malformed200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`this is not valid json {{{`))
	}))
	defer srv.Close()

	tokens := []string{"tok1", "tok2"}
	n, _ := newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", tokens, "inc-1")
	if len(failed) != len(tokens) {
		t.Fatalf("expected all %d tokens failed on malformed 200, got %d: %v", len(tokens), len(failed), failed)
	}
}

func TestSendPush_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tokens := []string{"tok1", "tok2"}
	n, _ := newTestNotifier(t, srv.URL)
	failed := n.SendPush(context.Background(), "ios", tokens, "inc-1")
	if len(failed) != len(tokens) {
		t.Fatalf("expected all %d tokens failed on 500, got %d: %v", len(tokens), len(failed), failed)
	}
}

func TestSendPush_TransportFailure(t *testing.T) {
	// Port 1 on loopback is never open — guaranteed connection refused.
	n, _ := newTestNotifier(t, "http://127.0.0.1:1")
	tokens := []string{"tok1", "tok2"}
	failed := n.SendPush(context.Background(), "ios", tokens, "inc-1")
	if len(failed) != len(tokens) {
		t.Fatalf("expected all %d tokens failed on transport error, got %d: %v", len(tokens), len(failed), failed)
	}
}

func TestSendPush_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until the request is cancelled
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before sending

	n, _ := newTestNotifier(t, srv.URL)
	tokens := []string{"tok1"}
	failed := n.SendPush(ctx, "ios", tokens, "inc-1")
	if len(failed) != len(tokens) {
		t.Fatalf("expected all %d tokens failed on cancelled context, got %d: %v", len(tokens), len(failed), failed)
	}
}
