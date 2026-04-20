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

	"github.com/wachd/wachd/internal/ai"
)

// ── SMSNotifier ───────────────────────────────────────────────────────────────

func TestNewSMSNotifier(t *testing.T) {
	n := NewSMSNotifier("ACtest", "authtoken", "+15550000000")
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
}

func TestSMSNotifier_NoPhone_Noop(t *testing.T) {
	n := NewSMSNotifier("ACtest", "authtoken", "+15550000000")
	member := makeTestMember()
	member.Phone = nil

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("expected no-op (nil) for member with no phone, got: %v", err)
	}
}

func TestSMSNotifier_EmptyPhone_Noop(t *testing.T) {
	n := NewSMSNotifier("ACtest", "authtoken", "+15550000000")
	member := makeTestMember()
	empty := ""
	member.Phone = &empty

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("expected no-op for empty phone, got: %v", err)
	}
}

func TestSMSNotifier_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Verify basic auth
		user, _, ok := r.BasicAuth()
		if !ok || user == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// Override Twilio URL by wrapping the client via the accountSid trick:
	// We create a notifier that points to the test server by setting
	// accountSid to something that makes the URL point to our test server.
	// Since the URL is constructed as:
	//   https://api.twilio.com/2010-04-01/Accounts/{accountSid}/Messages.json
	// we cannot easily redirect it without modifying production code.
	// Instead we test the no-phone and config-error paths above, and
	// verify the message builder logic separately below.
	//
	// For the actual HTTP send, we use the internal client field directly.
	phone := "+15551234567"
	member := makeTestMember()
	member.Phone = &phone

	n := &SMSNotifier{
		accountSid: "ACtest",
		authToken:  "authtoken",
		fromNumber: "+15550000000",
		client:     srv.Client(),
	}
	// Patch the internal Twilio API URL via a round-trip interceptor
	n.client = &http.Client{
		Transport: &smsTestTransport{srv: srv},
	}

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, makeTestAnalysis())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// smsTestTransport redirects all requests to the test server.
type smsTestTransport struct {
	srv *httptest.Server
}

func (t *smsTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Host = t.srv.Listener.Addr().String()
	req.URL.Scheme = "http"
	return http.DefaultTransport.RoundTrip(req)
}

func TestSMSNotifier_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"account not found"}`))
	}))
	defer srv.Close()

	phone := "+15551234567"
	member := makeTestMember()
	member.Phone = &phone

	n := &SMSNotifier{
		accountSid: "ACtest",
		authToken:  "authtoken",
		fromNumber: "+15550000000",
		client: &http.Client{
			Transport: &smsTestTransport{srv: srv},
		},
	}

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestSMSNotifier_BuildMessage_WithAnalysis(t *testing.T) {
	n := NewSMSNotifier("AC", "tok", "+1")
	incident := makeTestIncident()
	analysis := &ai.AnalysisResponse{RootCause: "Memory leak in the connection pool handler"}

	msg := n.buildMessage(incident, analysis)

	if !strings.Contains(msg, "[WACHD]") {
		t.Error("expected [WACHD] prefix")
	}
	if !strings.Contains(msg, strings.ToUpper(incident.Severity)) {
		t.Errorf("expected severity %q in message", incident.Severity)
	}
	if !strings.Contains(msg, incident.Title) {
		t.Error("expected incident title in message")
	}
	if !strings.Contains(msg, "Memory leak") {
		t.Error("expected root cause in message")
	}
}

func TestSMSNotifier_BuildMessage_LongRootCause_Truncated(t *testing.T) {
	n := NewSMSNotifier("AC", "tok", "+1")
	incident := makeTestIncident()
	analysis := &ai.AnalysisResponse{
		RootCause: strings.Repeat("x", 120), // > 80 chars
	}

	msg := n.buildMessage(incident, analysis)
	if !strings.Contains(msg, "...") {
		t.Error("expected truncation marker for long root cause")
	}
}

func TestSMSNotifier_BuildMessage_NoAnalysis(t *testing.T) {
	n := NewSMSNotifier("AC", "tok", "+1")
	msg := n.buildMessage(makeTestIncident(), nil)
	if !strings.Contains(msg, "[WACHD]") {
		t.Error("expected [WACHD] prefix even without analysis")
	}
}

func TestSMSNotifier_BuildMessage_EmptyRootCause(t *testing.T) {
	n := NewSMSNotifier("AC", "tok", "+1")
	analysis := &ai.AnalysisResponse{RootCause: ""}
	msg := n.buildMessage(makeTestIncident(), analysis)
	if strings.Contains(msg, " — ") {
		t.Error("should not include separator when root cause is empty")
	}
}

// ── VoiceNotifier ─────────────────────────────────────────────────────────────

func TestNewVoiceNotifier(t *testing.T) {
	n := NewVoiceNotifier("ACtest", "authtoken", "+15550000000", "https://twiml.example.com/alert")
	if n == nil {
		t.Fatal("expected non-nil notifier")
	}
}

func TestVoiceNotifier_NoPhone_Noop(t *testing.T) {
	n := NewVoiceNotifier("ACtest", "authtoken", "+15550000000", "https://twiml.example.com/alert")
	member := makeTestMember()
	member.Phone = nil

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("expected no-op for member with no phone: %v", err)
	}
}

func TestVoiceNotifier_EmptyPhone_Noop(t *testing.T) {
	n := NewVoiceNotifier("ACtest", "authtoken", "+15550000000", "https://twiml.example.com/alert")
	member := makeTestMember()
	empty := ""
	member.Phone = &empty

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("expected no-op for empty phone: %v", err)
	}
}

func TestVoiceNotifier_NoTwimlURL_Noop(t *testing.T) {
	n := NewVoiceNotifier("ACtest", "authtoken", "+15550000000", "")
	phone := "+15551234567"
	member := makeTestMember()
	member.Phone = &phone

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("expected no-op when twimlURL is empty: %v", err)
	}
}

func TestVoiceNotifier_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		user, _, ok := r.BasicAuth()
		if !ok || user == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	phone := "+15551234567"
	member := makeTestMember()
	member.Phone = &phone

	n := &VoiceNotifier{
		accountSid: "ACtest",
		authToken:  "authtoken",
		fromNumber: "+15550000000",
		twimlURL:   "https://twiml.example.com/alert",
		client: &http.Client{
			Transport: &smsTestTransport{srv: srv},
		},
	}

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVoiceNotifier_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid number"}`))
	}))
	defer srv.Close()

	phone := "+15551234567"
	member := makeTestMember()
	member.Phone = &phone

	n := &VoiceNotifier{
		accountSid: "ACtest",
		authToken:  "authtoken",
		fromNumber: "+15550000000",
		twimlURL:   "https://twiml.example.com/alert",
		client: &http.Client{
			Transport: &smsTestTransport{srv: srv},
		},
	}

	err := n.SendIncidentAlert(context.Background(), makeTestIncident(), member, nil)
	if err == nil {
		t.Error("expected error for 400 response")
	}
}

