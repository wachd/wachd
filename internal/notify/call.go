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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/store"
)

// VoiceNotifier places automated phone calls via Twilio.
// The call reads a short alert message using Twilio's TwiML.
type VoiceNotifier struct {
	accountSid string
	authToken  string
	fromNumber string
	twimlURL   string // URL that returns TwiML with the spoken message
	client     *http.Client
}

// NewVoiceNotifier creates a new Twilio voice call notifier.
// twimlURL is a publicly accessible URL returning TwiML — use a Twilio TwiML Bin or your own endpoint.
func NewVoiceNotifier(accountSid, authToken, fromNumber, twimlURL string) *VoiceNotifier {
	return &VoiceNotifier{
		accountSid: accountSid,
		authToken:  authToken,
		fromNumber: fromNumber,
		twimlURL:   twimlURL,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

// SendIncidentAlert places a voice call to the on-call engineer.
// No-ops silently if the user has no phone number configured.
func (v *VoiceNotifier) SendIncidentAlert(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) error {
	if onCallUser.Phone == nil || *onCallUser.Phone == "" {
		return nil
	}
	if v.twimlURL == "" {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Calls.json", v.accountSid)

	// Build a per-call TwiML URL that includes the alert message as a query param
	// so the TwiML bin can speak the incident title dynamically.
	twimlWithParams := fmt.Sprintf("%s?severity=%s&title=%s",
		v.twimlURL,
		url.QueryEscape(strings.ToUpper(incident.Severity)),
		url.QueryEscape(incident.Title),
	)

	form := url.Values{}
	form.Set("From", v.fromNumber)
	form.Set("To", *onCallUser.Phone)
	form.Set("Url", twimlWithParams)
	// Repeat the call twice if no answer, 30s timeout per attempt
	form.Set("MachineDetection", "Enable")

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("voice: create request: %w", err)
	}
	req.SetBasicAuth(v.accountSid, v.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("voice: twilio request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("voice: twilio returned %d: %s", resp.StatusCode, string(b))
	}

	return nil
}
