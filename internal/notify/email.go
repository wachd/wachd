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
	"net/smtp"
	"strings"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/store"
)

// EmailNotifier sends email notifications
type EmailNotifier struct {
	smtpHost string
	smtpPort string
	from     string
	username string
	password string
}

// NewEmailNotifier creates a new email notifier
func NewEmailNotifier(smtpHost, smtpPort, from, username, password string) *EmailNotifier {
	return &EmailNotifier{
		smtpHost: smtpHost,
		smtpPort: smtpPort,
		from:     from,
		username: username,
		password: password,
	}
}

// SendIncidentAlert sends an incident alert via email
func (e *EmailNotifier) SendIncidentAlert(ctx context.Context, incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) error {
	if e.smtpHost == "" {
		return fmt.Errorf("SMTP server not configured")
	}

	// Build email subject
	subject := fmt.Sprintf("[Wachd Alert] %s - %s", incident.Severity, incident.Title)

	// Build email body
	body := e.buildEmailBody(incident, onCallUser, analysis)

	// Prepare email message
	to := []string{onCallUser.Email}
	message := e.formatEmailMessage(to, subject, body)

	// Send email
	auth := smtp.PlainAuth("", e.username, e.password, e.smtpHost)
	addr := fmt.Sprintf("%s:%s", e.smtpHost, e.smtpPort)

	err := smtp.SendMail(addr, auth, e.from, to, []byte(message))
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// buildEmailBody creates the email body text
func (e *EmailNotifier) buildEmailBody(incident *store.Incident, onCallUser *store.TeamMember, analysis *ai.AnalysisResponse) string {
	var builder strings.Builder

	builder.WriteString("Wachd Alert Notification\n")
	builder.WriteString("========================\n\n")

	fmt.Fprintf(&builder, "Alert: %s\n", incident.Title)
	fmt.Fprintf(&builder, "Severity: %s\n", incident.Severity)
	fmt.Fprintf(&builder, "Source: %s\n", incident.Source)
	fmt.Fprintf(&builder, "Status: %s\n\n", incident.Status)

	if incident.Message != nil && *incident.Message != "" {
		fmt.Fprintf(&builder, "Message:\n%s\n\n", *incident.Message)
	}

	// Add root cause analysis if available
	if analysis != nil {
		builder.WriteString("ROOT CAUSE ANALYSIS\n")
		builder.WriteString("===================\n\n")
		fmt.Fprintf(&builder, "Probable Cause:\n%s\n\n", analysis.RootCause)
		fmt.Fprintf(&builder, "Suggested Action:\n%s\n\n", analysis.SuggestedAction)
		fmt.Fprintf(&builder, "Confidence: %s\n\n", analysis.Confidence)
	}

	fmt.Fprintf(&builder, "On-Call Engineer: %s (%s)\n", onCallUser.Name, onCallUser.Email)
	fmt.Fprintf(&builder, "Incident ID: %s\n", incident.ID.String())
	fmt.Fprintf(&builder, "Fired at: %s\n\n", incident.FiredAt.Format(time.RFC3339))

	builder.WriteString("---\n")
	builder.WriteString("This is an automated alert from Wachd.\n")

	return builder.String()
}

// formatEmailMessage formats an email message with headers
func (e *EmailNotifier) formatEmailMessage(to []string, subject, body string) string {
	headers := make(map[string]string)
	headers["From"] = e.from
	headers["To"] = strings.Join(to, ", ")
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/plain; charset=UTF-8"

	var message strings.Builder
	for key, value := range headers {
		fmt.Fprintf(&message, "%s: %s\r\n", key, value)
	}
	message.WriteString("\r\n")
	message.WriteString(body)

	return message.String()
}
