package notify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// RelayNotifier sends push notifications via the wachd.notify hosted relay
// (push.wachd.io). Used by self-hosted deployments that cannot hold the APNs
// and FCM credentials for the published app directly.
//
// Every request is signed with an Ed25519 private key. The private key never
// leaves the customer's cluster — the relay stores only the corresponding
// public key registered at deployment time.
//
// Required env vars:
//
//	WACHD_PUSH_RELAY_URL           — relay base URL (e.g. https://push.wachd.io)
//	WACHD_PUSH_RELAY_DEPLOYMENT_ID — UUID returned by POST /v1/register
//	WACHD_PUSH_RELAY_PRIVATE_KEY   — PEM-encoded Ed25519 private key (from k8s Secret)
type RelayNotifier struct {
	relayURL     string
	deploymentID string
	privateKey   ed25519.PrivateKey
	httpClient   *http.Client
}

// NewRelayNotifier creates a RelayNotifier from environment variables.
//
// Return values:
//   - nil, nil  — no relay env vars set; relay intentionally not configured
//   - nil, err  — some or all vars set but invalid; operator must fix config
//   - notifier, nil — fully configured and ready
func NewRelayNotifier() (*RelayNotifier, error) {
	relayURL := os.Getenv("WACHD_PUSH_RELAY_URL")
	deploymentID := os.Getenv("WACHD_PUSH_RELAY_DEPLOYMENT_ID")
	rawKey := os.Getenv("WACHD_PUSH_RELAY_PRIVATE_KEY")

	// Count how many of the three vars are set.
	set := 0
	for _, v := range []string{relayURL, deploymentID, rawKey} {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return nil, nil // relay not configured — normal for direct-mode deployments
	}
	if set < 3 {
		return nil, fmt.Errorf(
			"incomplete push relay config: set all three or none of "+
				"WACHD_PUSH_RELAY_URL, WACHD_PUSH_RELAY_DEPLOYMENT_ID, WACHD_PUSH_RELAY_PRIVATE_KEY "+
				"(%d/3 set)", set)
	}

	privateKey, err := parseRelayKey(rawKey)
	if err != nil {
		return nil, err
	}

	return &RelayNotifier{
		relayURL:     strings.TrimRight(relayURL, "/"),
		deploymentID: deploymentID,
		privateKey:   privateKey,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

type relayPayload struct {
	DeviceTokens []string `json:"device_tokens"`
	IncidentID   string   `json:"incident_id"`
	Platform     string   `json:"platform"`
}

// SendPush forwards a push notification request to the relay.
// Only device tokens and the incident UUID are sent — zero alert content.
// Returns the subset of tokens that failed.
func (n *RelayNotifier) SendPush(ctx context.Context, platform string, deviceTokens []string, incidentID string) []string {
	body, err := json.Marshal(relayPayload{
		DeviceTokens: deviceTokens,
		IncidentID:   incidentID,
		Platform:     platform,
	})
	if err != nil {
		return deviceTokens
	}

	timestamp := time.Now().Unix()
	sig, err := n.sign(timestamp, body)
	if err != nil {
		return deviceTokens
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.relayURL+"/v1/send", bytes.NewReader(body))
	if err != nil {
		return deviceTokens
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wachd-Deployment-ID", n.deploymentID)
	req.Header.Set("X-Wachd-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-Wachd-Signature", sig)

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return deviceTokens
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return deviceTokens
	}

	var result struct {
		Data struct {
			FailedTokens []string `json:"failed_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return deviceTokens
	}
	return result.Data.FailedTokens
}

// sign produces the Ed25519 signature for a /v1/send request.
// Message: "send:<unix_timestamp>:<hex(sha256(body))>"
func (n *RelayNotifier) sign(timestamp int64, body []byte) (string, error) {
	bodyHash := sha256.Sum256(body)
	msg := "send:" + strconv.FormatInt(timestamp, 10) + ":" + hex.EncodeToString(bodyHash[:])
	sig := ed25519.Sign(n.privateKey, []byte(msg))
	return base64.StdEncoding.EncodeToString(sig), nil
}

func parseRelayKey(pemData string) (ed25519.PrivateKey, error) {
	pemData = strings.ReplaceAll(pemData, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from WACHD_PUSH_RELAY_PRIVATE_KEY")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse relay private key: %w", err)
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("relay key must be Ed25519")
	}
	return edKey, nil
}
