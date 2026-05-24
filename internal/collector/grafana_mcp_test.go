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

func TestGrafanaMCPCollector_FetchErrorLogsAndMetrics(t *testing.T) {
	var sawAuth bool
	var listDatasourceCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer secret-token" {
			sawAuth = true
		}
		var req mcpRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-123")

		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"serverInfo": map[string]string{"name": "grafana", "version": "1.0"}})})
		case "notifications/initialized":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0"}`))
		case "tools/call":
			params := req.Params.(map[string]interface{})
			switch params["name"] {
			case toolListDatasources:
				listDatasourceCalls++
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": []map[string]string{{"uid": "loki-uid", "type": "loki", "name": "Loki"}, {"uid": "prom-uid", "type": "prometheus", "name": "Prometheus"}}})})
			case toolQueryLokiLogs:
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": map[string]any{"logs": []map[string]any{{"timestamp": "2026-01-01T10:00:00Z", "message": "fatal timeout", "level": "ERROR", "labels": map[string]string{"service": "api"}}}}})})
			case toolQueryPrometheus:
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": map[string]any{"series": []map[string]any{{"timestamp": "2026-01-01T10:00:00Z", "value": 1.5, "labels": map[string]string{"service": "api"}}}}})})
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := newGrafanaMCPCollectorWithClient(srv.URL, "secret-token", srv.Client())
	logs, err := c.FetchErrorLogs(context.Background(), "api", time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("FetchErrorLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "fatal timeout" {
		t.Fatalf("unexpected logs: %+v", logs)
	}
	metrics, err := c.FetchErrorRate(context.Background(), "api", 30*time.Minute)
	if err != nil {
		t.Fatalf("FetchErrorRate: %v", err)
	}
	if len(metrics) != 1 || metrics[0].Value != 1.5 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	if !sawAuth {
		t.Fatal("expected Authorization header to be sent to MCP endpoint")
	}
	if c.sessionID != "session-123" {
		t.Fatalf("expected session id to be captured, got %q", c.sessionID)
	}
	if listDatasourceCalls != 1 {
		t.Fatalf("expected list_datasources to be called once, got %d", listDatasourceCalls)
	}
}

func TestGrafanaMCPCollector_FetchErrorLogs_ParsesContentTextFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-abc")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"serverInfo": map[string]string{"name": "grafana", "version": "1.0"}})})
		case "notifications/initialized":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0"}`))
		case "tools/call":
			params := req.Params.(map[string]interface{})
			if params["name"] == toolListDatasources {
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"content": []map[string]string{{"type": "text", "text": `[{"uid":"loki-uid","type":"loki"}]`}}})})
				return
			}
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"content": []map[string]string{{"type": "text", "text": `{"logs":[{"timestamp":"2026-01-01T10:00:00Z","message":"boom","labels":{"service":"api"}}]}`}}})})
		}
	}))
	defer srv.Close()

	c := newGrafanaMCPCollectorWithClient(srv.URL, "", srv.Client())
	logs, err := c.FetchErrorLogs(context.Background(), "api", time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("FetchErrorLogs: %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0].Message, "boom") {
		t.Fatalf("unexpected logs from content fallback: %+v", logs)
	}
}

func TestGrafanaMCPCollector_FetchErrorRate_MissingDatasource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-x")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"serverInfo": map[string]string{"name": "grafana", "version": "1.0"}})})
		case "notifications/initialized":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0"}`))
		case "tools/call":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": []map[string]string{{"uid": "loki-uid", "type": "loki"}}})})
		}
	}))
	defer srv.Close()

	c := newGrafanaMCPCollectorWithClient(srv.URL, "", srv.Client())
	_, err := c.FetchErrorRate(context.Background(), "api", 30*time.Minute)
	if err == nil {
		t.Fatal("expected missing prometheus datasource error")
	}
}

func mustRawJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test json: %v", err)
	}
	return raw
}
