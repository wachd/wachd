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

// SMSNotifier sends SMS alerts via Twilio.
type SMSNotifier struct {
	accountSid string
	authToken  string
	fromNumber string
	client     *http.Client
}

// NewSMSNotifier creates a new Twilio SMS notifier.
func NewSMSNotifier(accountSid, authToken, fromNumber string) *SMSNotifier {
	return &SMSNotifier{
		accountSid: accountSid,
		authToken:  authToken,
		fromNumber: fromNumber,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

// SendIncidentAlert sends an SMS to the on-call engineer.
// No-ops silently if the user has no phone number configured.
func (s *SMSNotifier) SendIncidentAlert(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) error {
	if onCallUser.Phone == nil || *onCallUser.Phone == "" {
		return nil
	}

	body := s.buildMessage(incident, analysis)

	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", s.accountSid)

	form := url.Values{}
	form.Set("From", s.fromNumber)
	form.Set("To", *onCallUser.Phone)
	form.Set("Body", body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("sms: create request: %w", err)
	}
	req.SetBasicAuth(s.accountSid, s.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sms: twilio request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sms: twilio returned %d: %s", resp.StatusCode, string(b))
	}

	return nil
}

func (s *SMSNotifier) buildMessage(incident *store.Incident, analysis *ai.AnalysisResponse) string {
	cause := ""
	if analysis != nil && analysis.RootCause != "" {
		// Truncate to keep SMS under 160 chars after the prefix
		cause = analysis.RootCause
		if len(cause) > 80 {
			cause = cause[:77] + "..."
		}
		cause = " — " + cause
	}
	return fmt.Sprintf("[WACHD] %s: %s%s. Check Slack for details.", strings.ToUpper(incident.Severity), incident.Title, cause)
}
