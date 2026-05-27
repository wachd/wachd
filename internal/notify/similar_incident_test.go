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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSlackNotifierSendIncidentAlertWithSimilarIncludesBlock(t *testing.T) {
	var payload SlackMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode slack payload: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := newSlackNotifierWithClient(server.URL, "#alerts", server.Client())

	err := notifier.SendIncidentAlertWithSimilar(context.Background(), makeTestIncident(), makeTestMember(), makeTestAnalysis(), &SimilarIncident{
		Title:      "Payment timeout",
		Score:      0.84,
		Resolution: "rolled back v2.3.1",
		URL:        "https://wachd.example.com/incidents/abc",
		FiredAt:    time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("send slack alert: %v", err)
	}

	text := collectSlackText(payload.Blocks)

	for _, want := range []string{
		"Similar past incident (84%)",
		"Payment timeout",
		"Previous resolution",
		"rolled back v2.3.1",
		"<https://wachd.example.com/incidents/abc|view>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected Slack payload to contain %q, got:\n%s", want, text)
		}
	}
}

func TestEmailBuildBodyWithSimilarIncludesBlock(t *testing.T) {
	notifier := &EmailNotifier{}

	body := notifier.buildEmailBodyWithSimilar(makeTestIncident(), makeTestMember(), makeTestAnalysis(), &SimilarIncident{
		Title:      "Payment timeout",
		Score:      0.84,
		Resolution: "rolled back v2.3.1",
		URL:        "https://wachd.example.com/incidents/abc",
		FiredAt:    time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC),
	})

	for _, want := range []string{
		"Similar past incident",
		"Payment timeout",
		"Similarity: 84%",
		"Previous resolution: rolled back v2.3.1",
		"View: https://wachd.example.com/incidents/abc",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected email body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestSMSBuildMessageWithSimilarStaysWithin160Chars(t *testing.T) {
	notifier := &SMSNotifier{}

	incident := makeTestIncident()
	incident.Title = strings.Repeat("Checkout database outage ", 8)

	analysis := makeTestAnalysis()
	analysis.RootCause = strings.Repeat("database connection pool exhausted ", 8)

	message := notifier.buildMessageWithSimilar(incident, analysis, &SimilarIncident{
		Title: strings.Repeat("Checkout database pool incident ", 5),
		Score: 0.84,
	})

	if runeLen(message) > maxSMSIncidentAlertChars {
		t.Fatalf("expected SMS message to be <= %d chars, got %d: %q", maxSMSIncidentAlertChars, runeLen(message), message)
	}

	if !strings.Contains(message, "Similar:") {
		t.Fatalf("expected SMS message to include similar incident sentence, got %q", message)
	}

	if !strings.Contains(message, "84%") {
		t.Fatalf("expected SMS message to include similarity percent, got %q", message)
	}
}

func collectSlackText(blocks []Block) string {
	var parts []string

	for _, block := range blocks {
		if block.Text != nil {
			parts = append(parts, block.Text.Text)
		}
	}

	return strings.Join(parts, "\n")
}
