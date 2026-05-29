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
	"encoding/json"
	"fmt"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/safehttp"
	"github.com/wachd/wachd/internal/store"
	"net/http"
	"time"
)

// SlackNotifier sends notifications to Slack
type SlackNotifier struct {
	webhookURL string
	channel    string
	client     *http.Client
}

// SlackMessage represents a Slack webhook message
type SlackMessage struct {
	Channel string  `json:"channel,omitempty"`
	Text    string  `json:"text"`
	Blocks  []Block `json:"blocks,omitempty"`
}

// Block represents a Slack block
type Block struct {
	Type string `json:"type"`
	Text *Text  `json:"text,omitempty"`
}

// Text represents text in a Slack block
type Text struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewSlackNotifier creates a new Slack notifier.
func NewSlackNotifier(webhookURL, channel string) *SlackNotifier {
	return newSlackNotifierWithClient(webhookURL, channel, safehttp.WebhookClient(10*time.Second))
}

func newSlackNotifierWithClient(webhookURL, channel string, client *http.Client) *SlackNotifier {
	if client == nil {
		client = safehttp.WebhookClient(10 * time.Second)
	}

	return &SlackNotifier{
		webhookURL: webhookURL,
		channel:    channel,
		client:     client,
	}
}

// SendIncidentAlert sends an incident alert to Slack

func (s *SlackNotifier) SendIncidentAlert(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) error {
	return s.sendMessage(ctx, s.buildIncidentAlertMessage(incident, onCallUser, analysis))
}

func (s *SlackNotifier) buildIncidentAlertMessage(incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) SlackMessage {
	text := fmt.Sprintf("*Alert:* %s", incident.Title)

	onCallText := "Unassigned"
	if onCallUser != nil {
		onCallText = fmt.Sprintf("%s <%s>", onCallUser.Name, onCallUser.Email)
	}

	message := SlackMessage{
		Channel: s.channel,
		Text:    text,
		Blocks: []Block{
			{
				Type: "section",
				Text: &Text{
					Type: "mrkdwn",
					Text: text,
				},
			},
			{
				Type: "section",
				Text: &Text{
					Type: "mrkdwn",
					Text: fmt.Sprintf(
						"*Severity:* %s\n*Source:* %s\n*On-Call:* %s",
						incident.Severity,
						incident.Source,
						onCallText,
					),
				},
			},
		},
	}

	if incident.Message != nil && *incident.Message != "" {
		message.Blocks = append(message.Blocks, Block{
			Type: "section",
			Text: &Text{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Message:* %s", *incident.Message),
			},
		})
	}

	if analysis != nil {
		analysisText := fmt.Sprintf(
			"*Root Cause Analysis:*\n\n*Probable Cause:* %s\n\n*Suggested Action:* %s\n\n*Confidence:* %s",
			analysis.RootCause,
			analysis.SuggestedAction,
			analysis.Confidence,
		)

		message.Blocks = append(message.Blocks, Block{
			Type: "section",
			Text: &Text{
				Type: "mrkdwn",
				Text: analysisText,
			},
		})
	}

	message.Blocks = append(message.Blocks, Block{
		Type: "section",
		Text: &Text{
			Type: "mrkdwn",
			Text: fmt.Sprintf(
				"*Incident ID:* `%s`\n*Fired at:* %s",
				incident.ID.String(),
				incident.FiredAt.Format(time.RFC3339),
			),
		},
	})

	return message
}

// SendTestMessage sends a simple test message to verify the Slack webhook is working.
func (s *SlackNotifier) SendTestMessage(ctx context.Context) error {
	return s.sendMessage(ctx, SlackMessage{
		Channel: s.channel,
		Text:    "Wachd test notification — your Slack integration is working.",
	})
}

// sendMessage sends a message to Slack webhook
func (s *SlackNotifier) sendMessage(ctx context.Context, message SlackMessage) error {
	if s.webhookURL == "" {
		return fmt.Errorf("slack webhook URL not configured")
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal Slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send Slack message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned non-OK status: %d", resp.StatusCode)
	}

	return nil
}
