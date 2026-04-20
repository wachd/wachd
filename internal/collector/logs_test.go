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
	"testing"
	"time"
)

func TestNewLogsCollector(t *testing.T) {
	c := NewLogsCollector("http://loki.example.com")
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
}

func TestLogsCollector_FetchLogs_NoEndpoint(t *testing.T) {
	c := NewLogsCollector("")
	_, err := c.FetchLogs(context.Background(), `{app="api"}`, time.Now().Add(-time.Hour), time.Now(), 100)
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestLogsCollector_FetchLogs_Success(t *testing.T) {
	ts := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := LokiResponse{}
		resp.Status = "success"
		resp.Data.ResultType = "streams"
		resp.Data.Result = []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		}{
			{
				Stream: map[string]string{"service": "api", "level": "ERROR"},
				Values: [][]string{
					{ts.Format(time.RFC3339Nano), "connection refused"},
					{ts.Add(time.Second).Format(time.RFC3339Nano), "timeout error"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	logs, err := c.FetchLogs(context.Background(), `{service="api"}`, ts.Add(-time.Minute), ts.Add(time.Minute), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 log lines, got %d", len(logs))
	}
	if logs[0].Message != "connection refused" {
		t.Errorf("unexpected message: %q", logs[0].Message)
	}
	if logs[0].Level != "ERROR" {
		t.Errorf("expected level ERROR, got %q", logs[0].Level)
	}
}

func TestLogsCollector_FetchLogs_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	_, err := c.FetchLogs(context.Background(), `{app="x"}`, time.Now().Add(-time.Hour), time.Now(), 10)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestLogsCollector_FetchLogs_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	_, err := c.FetchLogs(context.Background(), `{app="x"}`, time.Now().Add(-time.Hour), time.Now(), 10)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLogsCollector_FetchLogs_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := LokiResponse{}
		resp.Status = "success"
		resp.Data.Result = nil
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	logs, err := c.FetchLogs(context.Background(), `{app="x"}`, time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(logs))
	}
}

func TestLogsCollector_FetchLogs_ShortValueEntry(t *testing.T) {
	// Values with < 2 elements should be skipped
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := LokiResponse{}
		resp.Status = "success"
		resp.Data.Result = []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		}{
			{
				Stream: map[string]string{},
				Values: [][]string{{"only-one-element"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	logs, err := c.FetchLogs(context.Background(), `{app="x"}`, time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs for short value entry, got %d", len(logs))
	}
}

func TestLogsCollector_FetchErrorLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the query contains the service name
		query := r.URL.Query().Get("query")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := LokiResponse{}
		resp.Status = "success"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewLogsCollector(srv.URL)
	_, err := c.FetchErrorLogs(context.Background(), "checkout-api", time.Now().Add(-time.Hour), time.Now(), 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
