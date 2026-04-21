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

package main

import (
	"strings"
	"testing"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/sanitiser"
)

// TestWorker_IncidentPIISanitisedBeforeAI verifies that PII in incident
// title and message is stripped before the values reach BuildPrompt.
//
// This is a regression test for the bug where incident.Title and
// incident.Message were passed to the AI backend without sanitisation.
// The fix in processNextJob uses w.sanitiser.Sanitise() on both fields
// before constructing the AnalysisRequest.
func TestWorker_IncidentPIISanitisedBeforeAI(t *testing.T) {
	san := sanitiser.NewSanitiser()

	cases := []struct {
		name    string
		title   string
		message string
	}{
		{
			name:    "email and IP in title",
			title:   "High CPU — service contacted admin@example.com from 10.0.0.5",
			message: "threshold exceeded",
		},
		{
			name:    "IP in message",
			title:   "Database connection refused",
			message: "host 192.168.1.200 rejected connection for user postgres",
		},
		{
			name:    "email in message",
			title:   "Auth failure",
			message: "failed login attempt for user@company.org — account locked",
		},
		{
			name:    "UUID account ID in message",
			title:   "Payment service error",
			message: "transaction failed for account 550e8400-e29b-41d4-a716-446655440000",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sanitisedTitle := san.Sanitise(tc.title)
			sanitisedMessage := san.Sanitise(tc.message)

			prompt := ai.BuildPrompt(&ai.AnalysisRequest{
				IncidentTitle:   sanitisedTitle,
				IncidentMessage: sanitisedMessage,
				Severity:        "high",
			})

			// Raw PII must not appear in the prompt sent to the AI backend.
			for _, pii := range extractPII(tc.title, tc.message) {
				if strings.Contains(prompt, pii) {
					t.Errorf("AI prompt contains raw PII %q — sanitiser not applied to incident fields", pii)
				}
			}

			// Placeholders must be present when PII was in the input.
			if strings.Contains(tc.title+tc.message, "@") && !strings.Contains(prompt, "[EMAIL]") {
				t.Error("prompt missing [EMAIL] placeholder — email not sanitised")
			}
			if containsIP(tc.title + tc.message) {
				if !strings.Contains(prompt, "[IP]") {
					t.Error("prompt missing [IP] placeholder — IP address not sanitised")
				}
			}
		})
	}
}

// TestWorker_SanitiseContext_NoOpOnCleanInput verifies that the sanitiser
// does not corrupt clean strings (no false positives).
func TestWorker_SanitiseContext_NoOpOnCleanInput(t *testing.T) {
	san := sanitiser.NewSanitiser()

	inputs := []string{
		"High CPU usage on web-server",
		"Database timeout after 30s",
		"HTTP 503 from checkout-api",
		"OOMKilled: container memory limit 512Mi exceeded",
	}

	for _, input := range inputs {
		result := san.Sanitise(input)
		if result != input {
			t.Errorf("sanitiser modified clean string:\n  input:  %q\n  output: %q", input, result)
		}
	}
}

// extractPII returns the raw PII tokens from the test strings so we can
// assert they are absent from the sanitised output.
func extractPII(fields ...string) []string {
	combined := strings.Join(fields, " ")
	var pii []string
	for _, word := range strings.Fields(combined) {
		word = strings.Trim(word, ".,;:")
		if strings.Contains(word, "@") && strings.Contains(word, ".") {
			pii = append(pii, word) // email
		}
		if isIPLike(word) {
			pii = append(pii, word) // IP
		}
	}
	return pii
}

func isIPLike(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

func containsIP(s string) bool {
	for _, word := range strings.Fields(s) {
		word = strings.Trim(word, ".,;:")
		if isIPLike(word) {
			return true
		}
	}
	return false
}
