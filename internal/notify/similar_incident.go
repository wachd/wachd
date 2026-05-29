// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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
	"net/smtp"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/store"
)

const maxSMSIncidentAlertChars = 160

// SimilarIncident is the small notification-safe representation of the best
// historical incident match.
type SimilarIncident struct {
	Title      string
	Score      float64
	Resolution string
	URL        string
	FiredAt    time.Time
}

// SendIncidentAlertWithSimilar sends the normal Slack incident alert and, when
// present, includes the most similar past incident.

func (s *SlackNotifier) SendIncidentAlertWithSimilar(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *SimilarIncident) error {
	if similar == nil {
		return s.SendIncidentAlert(ctx, incident, onCallUser, analysis)
	}

	message := s.buildIncidentAlertMessage(incident, onCallUser, analysis)

	message.Blocks = insertSlackBlockBeforeIncidentID(message.Blocks, Block{
		Type: "section",
		Text: &Text{
			Type: "mrkdwn",
			Text: formatSimilarIncidentSlack(similar),
		},
	})

	return s.sendMessage(ctx, message)
}

func insertSlackBlockBeforeIncidentID(blocks []Block, block Block) []Block {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Text != nil && strings.Contains(blocks[i].Text.Text, "*Incident ID:*") {
			updated := make([]Block, 0, len(blocks)+1)
			updated = append(updated, blocks[:i]...)
			updated = append(updated, block)
			updated = append(updated, blocks[i:]...)
			return updated
		}
	}

	return append(blocks, block)
}

// SendIncidentAlertWithSimilar sends the normal email alert and, when present,
// includes the most similar past incident.
func (e *EmailNotifier) SendIncidentAlertWithSimilar(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *SimilarIncident) error {
	if similar == nil {
		return e.SendIncidentAlert(ctx, incident, onCallUser, analysis)
	}

	if e.smtpHost == "" {
		return fmt.Errorf("SMTP server not configured")
	}
	if onCallUser == nil {
		return fmt.Errorf("on-call user is required")
	}

	subject := fmt.Sprintf("[Wachd Alert] %s - %s", incident.Severity, incident.Title)
	body := e.buildEmailBodyWithSimilar(incident, onCallUser, analysis, similar)

	to := []string{onCallUser.Email}
	message := e.formatEmailMessage(to, subject, body)

	auth := smtp.PlainAuth("", e.username, e.password, e.smtpHost)
	addr := fmt.Sprintf("%s:%s", e.smtpHost, e.smtpPort)

	if err := smtp.SendMail(addr, auth, e.from, to, []byte(message)); err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

func (e *EmailNotifier) buildEmailBodyWithSimilar(incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *SimilarIncident) string {
	body := e.buildEmailBody(incident, onCallUser, analysis)
	if similar == nil {
		return body
	}

	section := formatSimilarIncidentEmail(similar)

	footer := "---\n"
	if idx := strings.Index(body, footer); idx >= 0 {
		return body[:idx] + section + "\n" + body[idx:]
	}

	return strings.TrimRight(body, "\n") + "\n\n" + section
}

// SendIncidentAlertWithSimilar sends the normal SMS alert and, when present,
// appends one short similar-incident sentence capped to 160 characters.
func (s *SMSNotifier) SendIncidentAlertWithSimilar(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse, similar *SimilarIncident) error {
	if similar == nil {
		return s.SendIncidentAlert(ctx, incident, onCallUser, analysis)
	}

	if onCallUser == nil || onCallUser.Phone == nil || *onCallUser.Phone == "" {
		return nil
	}

	body := s.buildMessageWithSimilar(incident, analysis, similar)

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

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
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

func (s *SMSNotifier) buildMessageWithSimilar(incident *store.Incident, analysis *ai.AnalysisResponse, similar *SimilarIncident) string {
	base := s.buildMessage(incident, analysis)
	if similar == nil {
		return truncateRunes(base, maxSMSIncidentAlertChars)
	}

	title := strings.TrimSpace(similar.Title)
	if title == "" {
		title = "past incident"
	}

	suffix := fmt.Sprintf(" Similar: %s (%s).", truncateRunes(title, 34), formatSimilarityPercent(similar.Score))

	if runeLen(base)+runeLen(suffix) <= maxSMSIncidentAlertChars {
		return base + suffix
	}

	allowedBase := maxSMSIncidentAlertChars - runeLen(suffix)
	if allowedBase <= 0 {
		return truncateRunes(strings.TrimSpace(suffix), maxSMSIncidentAlertChars)
	}

	return truncateRunes(base, allowedBase) + suffix
}

func formatSimilarIncidentSlack(similar *SimilarIncident) string {
	title := strings.TrimSpace(similar.Title)
	if title == "" {
		title = "Untitled incident"
	}

	text := fmt.Sprintf("*Similar past incident (%s):* %s", formatSimilarityPercent(similar.Score), title)

	if !similar.FiredAt.IsZero() {
		text += fmt.Sprintf(" - %s", similar.FiredAt.Format("Jan 2"))
	}

	if strings.TrimSpace(similar.Resolution) != "" {
		text += fmt.Sprintf("\n*Previous resolution:* %s", strings.TrimSpace(similar.Resolution))
	}

	if strings.TrimSpace(similar.URL) != "" {
		text += fmt.Sprintf("  ->  <%s|view>", strings.TrimSpace(similar.URL))
	}

	return text
}

func formatSimilarIncidentEmail(similar *SimilarIncident) string {
	var b strings.Builder

	title := strings.TrimSpace(similar.Title)
	if title == "" {
		title = "Untitled incident"
	}

	b.WriteString("Similar past incident\n")
	b.WriteString("---------------------\n")
	b.WriteString("Title: ")
	b.WriteString(title)
	b.WriteByte('\n')

	b.WriteString("Similarity: ")
	b.WriteString(formatSimilarityPercent(similar.Score))
	b.WriteByte('\n')

	if !similar.FiredAt.IsZero() {
		b.WriteString("Fired at: ")
		b.WriteString(similar.FiredAt.Format(time.RFC3339))
		b.WriteByte('\n')
	}

	if strings.TrimSpace(similar.Resolution) != "" {
		b.WriteString("Previous resolution: ")
		b.WriteString(strings.TrimSpace(similar.Resolution))
		b.WriteByte('\n')
	}

	if strings.TrimSpace(similar.URL) != "" {
		b.WriteString("View: ")
		b.WriteString(strings.TrimSpace(similar.URL))
		b.WriteByte('\n')
	}

	b.WriteString("\n")

	return b.String()
}

func formatSimilarityPercent(score float64) string {
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return fmt.Sprintf("%.0f%%", score*100)
}

func truncateRunes(value string, max int) string {
	value = strings.TrimSpace(value)

	if max <= 0 {
		return ""
	}

	if utf8.RuneCountInString(value) <= max {
		return value
	}

	runes := []rune(value)
	if max <= 3 {
		return string(runes[:max])
	}

	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}
