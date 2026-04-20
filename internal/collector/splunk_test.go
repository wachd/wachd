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

package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewSplunkCollector(t *testing.T) {
	c := NewSplunkCollector("https://splunk.example.com:8089", "token123")
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	// Trailing slash should be stripped
	c2 := NewSplunkCollector("https://splunk.example.com:8089/", "tok")
	if strings.HasSuffix(c2.endpoint, "/") {
		t.Error("endpoint should have trailing slash stripped")
	}
}

func TestNewSplunkCollectorBasicAuth(t *testing.T) {
	c := NewSplunkCollectorBasicAuth("https://splunk.example.com:8089", "admin", "changeme")
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.authType != "basic" {
		t.Errorf("expected authType=basic, got %q", c.authType)
	}
}

func TestSplunkCollector_FetchLogs_MissingConfig(t *testing.T) {
	tests := []struct {
		endpoint string
		token    string
	}{
		{"", "token"},
		{"https://splunk.example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		c := NewSplunkCollector(tt.endpoint, tt.token)
		_, err := c.FetchLogs(context.Background(), "svc", time.Now().Add(-time.Hour), time.Now(), 10)
		if err == nil {
			t.Errorf("expected error for endpoint=%q token=%q", tt.endpoint, tt.token)
		}
	}
}

func TestSplunkCollector_FetchLogs_Success(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Verify auth header present
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"_time":    ts.Format(time.RFC3339),
					"_raw":     "ERROR: database connection failed",
					"source":   "checkout-api",
					"host":     "pod-abc123",
					"level":    "ERROR",
					"severity": "error",
				},
				{
					"_time":  ts.Add(time.Second).Format(time.RFC3339),
					"_raw":   "WARN: retry attempt 1",
					"source": "checkout-api",
					"host":   "pod-abc123",
					"level":  "WARN",
				},
			},
		})
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "splunk-token")
	logs, err := c.FetchLogs(context.Background(), "checkout-api",
		ts.Add(-time.Minute), ts.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(logs))
	}
	if logs[0].Message != "ERROR: database connection failed" {
		t.Errorf("unexpected message: %q", logs[0].Message)
	}
	if logs[0].Level != "ERROR" {
		t.Errorf("unexpected level: %q", logs[0].Level)
	}
}

func TestSplunkCollector_FetchLogs_FallbackSeverityField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"_time":    time.Now().Format(time.RFC3339),
					"_raw":     "error log",
					"severity": "error", // no "level" field
				},
			},
		})
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "token")
	logs, err := c.FetchLogs(context.Background(), "svc",
		time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Level != "error" {
		t.Errorf("expected level=error from severity fallback, got %q", logs[0].Level)
	}
}

func TestSplunkCollector_FetchLogs_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "bad-token")
	_, err := c.FetchLogs(context.Background(), "svc",
		time.Now().Add(-time.Hour), time.Now(), 10)
	if err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestSplunkCollector_FetchLogs_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json{{{"))
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "token")
	_, err := c.FetchLogs(context.Background(), "svc",
		time.Now().Add(-time.Hour), time.Now(), 10)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSplunkCollector_FetchNotableEvents_MissingConfig(t *testing.T) {
	c := NewSplunkCollector("", "")
	_, err := c.FetchNotableEvents(context.Background(), "svc", time.Now().Add(-time.Hour), 10)
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestSplunkCollector_FetchNotableEvents_Success(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"_time":   ts.Format(time.RFC3339),
					"_raw":    "Notable: service degradation detected",
					"source":  "notable",
					"host":    "splunk-search-head",
					"service": "checkout-api",
				},
			},
		})
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "token")
	events, err := c.FetchNotableEvents(context.Background(), "checkout-api", ts.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Raw != "Notable: service degradation detected" {
		t.Errorf("unexpected raw: %q", events[0].Raw)
	}
}

func TestSplunkCollector_BasicAuth_SetsHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}})
	}))
	defer srv.Close()

	c := NewSplunkCollectorBasicAuth(srv.URL, "admin", "secret")
	_, _ = c.FetchLogs(context.Background(), "svc", time.Now().Add(-time.Hour), time.Now(), 10)

	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("expected Basic auth header, got %q", gotAuth)
	}
}

func TestSplunkCollector_BearerAuth_SetsHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": []interface{}{}})
	}))
	defer srv.Close()

	c := NewSplunkCollector(srv.URL, "mytoken")
	_, _ = c.FetchLogs(context.Background(), "svc", time.Now().Add(-time.Hour), time.Now(), 10)

	if gotAuth != "Bearer mytoken" {
		t.Errorf("expected 'Bearer mytoken', got %q", gotAuth)
	}
}
