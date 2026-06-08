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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wachd/wachd/internal/agent"
)

// captureServer returns a test HTTP server that records the last request it received.
type captureServer struct {
	*httptest.Server
	lastMethod string
	lastPath   string
	lastBody   []byte
	lastCT     string
}

func newCaptureServer(statusCode int) *captureServer {
	cs := &captureServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.lastMethod = r.Method
		cs.lastPath = r.URL.Path
		cs.lastCT = r.Header.Get("Content-Type")
		cs.lastBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(statusCode)
	}))
	return cs
}

func TestForwarder_SendUsesCorrectURL(t *testing.T) {
	srv := newCaptureServer(http.StatusOK)
	defer srv.Close()

	f := newForwarder(srv.URL, "team-abc", "mysecret")
	ev := agent.Event{Title: "test alert", Severity: "high", Source: "kubescape"}

	if err := f.send(context.Background(), ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "/api/v1/webhook/team-abc/mysecret"
	if srv.lastPath != want {
		t.Errorf("want path %q, got %q", want, srv.lastPath)
	}
	if srv.lastMethod != http.MethodPost {
		t.Errorf("want POST, got %s", srv.lastMethod)
	}
}

func TestForwarder_SendSetsContentType(t *testing.T) {
	srv := newCaptureServer(http.StatusOK)
	defer srv.Close()

	f := newForwarder(srv.URL, "t1", "s1")
	f.send(context.Background(), agent.Event{Title: "x", Severity: "high", Source: "kubescape"}) //nolint:errcheck

	if !strings.Contains(srv.lastCT, "application/json") {
		t.Errorf("Content-Type must be application/json, got %q", srv.lastCT)
	}
}

func TestForwarder_SendPayloadShape(t *testing.T) {
	srv := newCaptureServer(http.StatusOK)
	defer srv.Close()

	f := newForwarder(srv.URL, "t1", "s1")
	ev := agent.Event{
		Title:    "Kubescape: 3 vulnerability finding(s) in production/payments-api",
		Severity: "critical",
		Source:   "kubescape",
		Details:  "3 critical CVEs in runtime packages",
		Labels:   map[string]string{"env": "production"},
	}
	if err := f.send(context.Background(), ev); err != nil {
		t.Fatalf("send error: %v", err)
	}

	var p webhookPayload
	if err := json.Unmarshal(srv.lastBody, &p); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if p.Title != ev.Title {
		t.Errorf("title: want %q, got %q", ev.Title, p.Title)
	}
	if p.Severity != ev.Severity {
		t.Errorf("severity: want %q, got %q", ev.Severity, p.Severity)
	}
	if p.Source != ev.Source {
		t.Errorf("source: want %q, got %q", ev.Source, p.Source)
	}
	if p.Message != ev.Details {
		t.Errorf("message: want %q, got %q", ev.Details, p.Message)
	}
	if p.Labels["env"] != "production" {
		t.Errorf("labels not forwarded, got %v", p.Labels)
	}
}

func TestForwarder_SendErrorOnNon2xx(t *testing.T) {
	srv := newCaptureServer(http.StatusInternalServerError)
	defer srv.Close()

	f := newForwarder(srv.URL, "t1", "s1")
	err := f.send(context.Background(), agent.Event{Title: "x", Severity: "high", Source: "kubescape"})
	if err == nil {
		t.Fatal("expected error for 500 status, got nil")
	}
}

func TestForwarder_SendErrorOn3xx(t *testing.T) {
	srv := newCaptureServer(http.StatusFound)
	defer srv.Close()

	f := newForwarder(srv.URL, "t1", "s1")
	err := f.send(context.Background(), agent.Event{Title: "x", Severity: "high", Source: "kubescape"})
	if err == nil {
		t.Fatal("expected error for 302 redirect, got nil")
	}
}

func TestForwarder_SendErrorOnUnreachableEndpoint(t *testing.T) {
	// Point at a port nothing is listening on.
	f := newForwarder("http://127.0.0.1:1", "t1", "s1")
	err := f.send(context.Background(), agent.Event{Title: "x", Severity: "high", Source: "kubescape"})
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
}

func TestForwarder_SendRespectsContextCancellation(t *testing.T) {
	// Slow server that never responds before ctx is cancelled.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	f := newForwarder(slow.URL, "t1", "s1")
	err := f.send(ctx, agent.Event{Title: "x", Severity: "high", Source: "kubescape"})
	if err == nil {
		t.Fatal("expected error when context is already cancelled")
	}
}

func TestForwarder_SecretNotInLogs(t *testing.T) {
	// Verify the URL built by send contains the secret path component.
	// This test is a documentation test — it confirms we know the secret
	// is in the URL, and callers should never log the full URL.
	srv := newCaptureServer(http.StatusOK)
	defer srv.Close()

	f := newForwarder(srv.URL, "t1", "supersecret")
	f.send(context.Background(), agent.Event{Title: "x", Severity: "high", Source: "kubescape"}) //nolint:errcheck

	if !strings.Contains(srv.lastPath, "supersecret") {
		t.Error("secret must appear in path — if this fails, check URL construction in send()")
	}
}
