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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/store"
)

func makeTestIncident() *store.Incident {
	msg := "CPU usage at 95%"
	return &store.Incident{
		ID:       uuid.New(),
		TeamID:   uuid.New(),
		Title:    "High CPU Usage",
		Message:  &msg,
		Severity: "high",
		Status:   "open",
		Source:   "grafana",
		FiredAt:  time.Now(),
	}
}

func makeTestMember() *store.TeamMember {
	return &store.TeamMember{
		ID:     uuid.New(),
		Name:   "Alice Smith",
		Email:  "alice@example.com",
		TeamID: uuid.New(),
		Role:   "responder",
	}
}

func makeTestAnalysis() *ai.AnalysisResponse {
	return &ai.AnalysisResponse{
		RootCause:       "Memory leak in connection pool",
		SuggestedAction: "Restart affected pods",
		Confidence:      "high",
	}
}

// ── SlackNotifier ─────────────────────────────────────────────────────────────

func TestNewSlackNotifier(t *testing.T) {
	n := NewSlackNotifier("https://hooks.slack.com/services/test", "#alerts")
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
}

func TestSlackNotifier_SendIncidentAlert_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(srv.URL, "#alerts")
	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), makeTestMember(), makeTestAnalysis())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlackNotifier_SendIncidentAlert_NoAnalysis(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewSlackNotifier(srv.URL, "#alerts")
	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), makeTestMember(), nil)
	if err != nil {
		t.Fatalf("unexpected error with nil analysis: %v", err)
	}
}

func TestSlackNotifier_SendIncidentAlert_NoMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	incident := makeTestIncident()
	incident.Message = nil

	n := NewSlackNotifier(srv.URL, "#alerts")
	err := n.SendIncidentAlert(context.Background(), incident, makeTestMember(), nil)
	if err != nil {
		t.Fatalf("unexpected error with nil message: %v", err)
	}
}

func TestSlackNotifier_SendIncidentAlert_EmptyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	incident := makeTestIncident()
	empty := ""
	incident.Message = &empty

	n := NewSlackNotifier(srv.URL, "#alerts")
	err := n.SendIncidentAlert(context.Background(), incident, makeTestMember(), nil)
	if err != nil {
		t.Fatalf("unexpected error with empty message: %v", err)
	}
}

func TestSlackNotifier_SendMessage_NoWebhook(t *testing.T) {
	n := NewSlackNotifier("", "#alerts")
	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), makeTestMember(), nil)
	if err == nil {
		t.Error("expected error for empty webhook URL")
	}
}

func TestSlackNotifier_SendMessage_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	n := NewSlackNotifier(srv.URL, "#alerts")
	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), makeTestMember(), nil)
	if err == nil {
		t.Error("expected error for non-200 response")
	}
}

// ── EmailNotifier (pure logic) ────────────────────────────────────────────────

func TestEmailNotifier_BuildEmailBody_WithAnalysis(t *testing.T) {
	e := NewEmailNotifier("smtp.example.com", "587", "alerts@example.com", "user", "pass")
	incident := makeTestIncident()
	member := makeTestMember()
	analysis := makeTestAnalysis()

	body := e.buildEmailBody(incident, member, analysis)

	for _, want := range []string{
		"High CPU Usage",
		"high",
		"grafana",
		"ROOT CAUSE ANALYSIS",
		"Memory leak in connection pool",
		"Restart affected pods",
		"Alice Smith",
		"alice@example.com",
		"Wachd Alert Notification",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("email body missing %q", want)
		}
	}
}

func TestEmailNotifier_BuildEmailBody_NoAnalysis(t *testing.T) {
	e := NewEmailNotifier("smtp.example.com", "587", "alerts@example.com", "user", "pass")
	body := e.buildEmailBody(makeTestIncident(), makeTestMember(), nil)

	if strings.Contains(body, "ROOT CAUSE ANALYSIS") {
		t.Error("body should not contain ROOT CAUSE ANALYSIS when analysis is nil")
	}
	if !strings.Contains(body, "High CPU Usage") {
		t.Error("body should still contain incident title")
	}
}

func TestEmailNotifier_BuildEmailBody_NilMessage(t *testing.T) {
	e := NewEmailNotifier("smtp.example.com", "587", "alerts@example.com", "user", "pass")
	incident := makeTestIncident()
	incident.Message = nil

	// Should not panic
	body := e.buildEmailBody(incident, makeTestMember(), nil)
	if body == "" {
		t.Error("expected non-empty body")
	}
}

func TestEmailNotifier_FormatEmailMessage(t *testing.T) {
	e := NewEmailNotifier("smtp.example.com", "587", "from@example.com", "user", "pass")
	to := []string{"oncall@example.com"}
	subject := "[Wachd Alert] critical - DB down"
	body := "Alert body here."

	msg := e.formatEmailMessage(to, subject, body)

	for _, want := range []string{
		"From: from@example.com",
		"To: oncall@example.com",
		"Subject: [Wachd Alert] critical - DB down",
		"MIME-Version: 1.0",
		"Content-Type: text/plain",
		"Alert body here.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("email message missing %q", want)
		}
	}
}

func TestEmailNotifier_SendIncidentAlert_NoSMTP(t *testing.T) {
	e := NewEmailNotifier("", "587", "from@example.com", "user", "pass")
	err := e.SendIncidentAlert(context.Background(), makeTestIncident(), makeTestMember(), nil)
	if err == nil {
		t.Error("expected error when SMTP host not configured")
	}
}
