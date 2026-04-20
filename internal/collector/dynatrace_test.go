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

func TestNewDynatraceCollector(t *testing.T) {
	c := NewDynatraceCollector("https://abc.live.dynatrace.com", "dt-token")
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
}

func TestDynatraceCollector_FetchProblems_MissingConfig(t *testing.T) {
	tests := []struct {
		endpoint string
		token    string
	}{
		{"", "token"},
		{"https://example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		c := NewDynatraceCollector(tt.endpoint, tt.token)
		_, err := c.FetchProblems(context.Background(), "svc", time.Now().Add(-time.Hour), 10)
		if err == nil {
			t.Errorf("expected error for endpoint=%q token=%q", tt.endpoint, tt.token)
		}
	}
}

func TestDynatraceCollector_FetchProblems_Success(t *testing.T) {
	start := time.Now().Add(-time.Hour)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"problems": []map[string]interface{}{
				{
					"problemId":     "P-001",
					"title":         "High error rate",
					"status":        "OPEN",
					"severityLevel": "AVAILABILITY",
					"startTime":     start.UnixMilli(),
					"affectedEntities": []map[string]string{
						{"name": "checkout-api"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	problems, err := c.FetchProblems(context.Background(), "checkout-api", start, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d", len(problems))
	}
	if problems[0].ID != "P-001" {
		t.Errorf("unexpected problem ID: %q", problems[0].ID)
	}
	if problems[0].Status != "OPEN" {
		t.Errorf("unexpected status: %q", problems[0].Status)
	}
	if len(problems[0].AffectedEntities) != 1 || problems[0].AffectedEntities[0] != "checkout-api" {
		t.Errorf("unexpected affected entities: %v", problems[0].AffectedEntities)
	}
}

func TestDynatraceCollector_FetchProblems_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	_, err := c.FetchProblems(context.Background(), "svc", time.Now().Add(-time.Hour), 10)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestDynatraceCollector_FetchLogs_MissingConfig(t *testing.T) {
	c := NewDynatraceCollector("", "")
	_, err := c.FetchLogs(context.Background(), "svc", time.Now().Add(-time.Hour), time.Now(), 10)
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestDynatraceCollector_FetchLogs_Success(t *testing.T) {
	ts := time.Now().Add(-5 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"timestamp":  ts.UnixMilli(),
					"content":    "NullPointerException in checkout",
					"status":     "ERROR",
					"attributes": map[string]string{"env": "prod"},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	logs, err := c.FetchLogs(context.Background(), "checkout-api", ts.Add(-time.Minute), ts.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Message != "NullPointerException in checkout" {
		t.Errorf("unexpected message: %q", logs[0].Message)
	}
	if logs[0].Level != "ERROR" {
		t.Errorf("unexpected level: %q", logs[0].Level)
	}
	if logs[0].Labels["service"] != "checkout-api" {
		t.Errorf("expected service label, got: %v", logs[0].Labels)
	}
}

func TestDynatraceCollector_FetchLogs_NilAttributes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"timestamp":  time.Now().UnixMilli(),
					"content":    "error log",
					"status":     "WARN",
					"attributes": nil,
				},
			},
		})
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	logs, err := c.FetchLogs(context.Background(), "svc", time.Now().Add(-time.Hour), time.Now(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	// Labels should be an initialized map even when attributes is nil
	if logs[0].Labels == nil {
		t.Error("expected non-nil Labels map")
	}
}

func TestDynatraceCollector_FetchMetrics_MissingConfig(t *testing.T) {
	c := NewDynatraceCollector("", "")
	_, err := c.FetchMetrics(context.Background(), "svc", "builtin:error.rate",
		time.Now().Add(-time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestDynatraceCollector_FetchMetrics_Success(t *testing.T) {
	ts := time.Now().Add(-10 * time.Minute)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resolution": "1m",
			"result": []map[string]interface{}{
				{
					"metricId": "builtin:service.errors.total.rate",
					"data": []map[string]interface{}{
						{
							"timestamps": []int64{ts.UnixMilli(), ts.Add(time.Minute).UnixMilli()},
							"values":     []float64{0.5, 1.2},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	points, err := c.FetchMetrics(context.Background(), "checkout-api",
		"builtin:service.errors.total.rate", ts.Add(-time.Minute), ts.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 metric points, got %d", len(points))
	}
	if points[0].Value != 0.5 {
		t.Errorf("unexpected value: %f", points[0].Value)
	}
}

func TestDynatraceCollector_FetchMetrics_MismatchedTimestampsValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"metricId": "builtin:error.rate",
					"data": []map[string]interface{}{
						{
							// More timestamps than values — extra timestamps skipped
							"timestamps": []int64{
								time.Now().UnixMilli(),
								time.Now().Add(time.Minute).UnixMilli(),
							},
							"values": []float64{1.0},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewDynatraceCollector(srv.URL, "dt-token")
	points, err := c.FetchMetrics(context.Background(), "svc", "builtin:error.rate",
		time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only 1 point should be returned (stops when values run out)
	if len(points) != 1 {
		t.Errorf("expected 1 point for mismatched lengths, got %d", len(points))
	}
}
