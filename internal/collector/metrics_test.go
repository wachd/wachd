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

// ── NewMetricsCollector ───────────────────────────────────────────────────────

func TestNewMetricsCollector_Empty(t *testing.T) {
	_, err := NewMetricsCollector("")
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestNewMetricsCollector_Valid(t *testing.T) {
	c, err := NewMetricsCollector("http://prometheus:9090")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
}

// ── promMatrixResponse builds a Prometheus /api/v1/query_range response. ──────

func promMatrixResponse(values [][]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "matrix",
			"result": []map[string]interface{}{
				{
					"metric": map[string]string{"service": "test"},
					"values": values,
				},
			},
		},
	}
}

func promVectorResponse(ts float64, val string) map[string]interface{} {
	return map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []map[string]interface{}{
				{
					"metric": map[string]string{"service": "test"},
					"value":  []interface{}{ts, val},
				},
			},
		},
	}
}

// ── FetchMetricHistory ────────────────────────────────────────────────────────

func TestFetchMetricHistory_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := promMatrixResponse([][]interface{}{
			{1609459200.0, "1.5"},
			{1609459260.0, "2.0"},
		})
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, err := NewMetricsCollector(srv.URL)
	if err != nil {
		t.Fatalf("NewMetricsCollector: %v", err)
	}

	now := time.Now()
	points, err := c.FetchMetricHistory(context.Background(), "test_metric", now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 2 {
		t.Errorf("expected 2 points, got %d", len(points))
	}
	if points[0].Value != 1.5 {
		t.Errorf("expected value 1.5, got %v", points[0].Value)
	}
	if points[1].Value != 2.0 {
		t.Errorf("expected value 2.0, got %v", points[1].Value)
	}
	// Labels should be populated
	if points[0].Labels["service"] != "test" {
		t.Errorf("expected label service=test, got %v", points[0].Labels)
	}
}

func TestFetchMetricHistory_MultipleStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"service": "svc1"},
						"values": [][]interface{}{{1609459200.0, "1.0"}},
					},
					{
						"metric": map[string]string{"service": "svc2"},
						"values": [][]interface{}{{1609459200.0, "2.0"}, {1609459260.0, "3.0"}},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	now := time.Now()
	points, err := c.FetchMetricHistory(context.Background(), "q", now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 1 + 2 = 3 total points across streams
	if len(points) != 3 {
		t.Errorf("expected 3 points from 2 streams, got %d", len(points))
	}
}

func TestFetchMetricHistory_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	now := time.Now()
	_, err := c.FetchMetricHistory(context.Background(), "q", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error for server 500")
	}
}

func TestFetchMetricHistory_UnexpectedResultType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return a vector instead of matrix
		body := promVectorResponse(1609459200.0, "42.0")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	now := time.Now()
	_, err := c.FetchMetricHistory(context.Background(), "q", now.Add(-time.Hour), now, time.Minute)
	if err == nil {
		t.Error("expected error for non-matrix result type")
	}
}

func TestFetchMetricHistory_EmptyMatrix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result":     []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	now := time.Now()
	points, err := c.FetchMetricHistory(context.Background(), "q", now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 points for empty result, got %d", len(points))
	}
}

// ── FetchCurrentValue ─────────────────────────────────────────────────────────

func TestFetchCurrentValue_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := promVectorResponse(1609459200.0, "42.5")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	val, labels, err := c.FetchCurrentValue(context.Background(), "test_metric")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42.5 {
		t.Errorf("expected 42.5, got %v", val)
	}
	if labels["service"] != "test" {
		t.Errorf("expected label service=test, got %v", labels)
	}
}

func TestFetchCurrentValue_EmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result":     []interface{}{},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	_, _, err := c.FetchCurrentValue(context.Background(), "empty_metric")
	if err == nil {
		t.Error("expected error for empty vector result")
	}
}

func TestFetchCurrentValue_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	_, _, err := c.FetchCurrentValue(context.Background(), "q")
	if err == nil {
		t.Error("expected error for server 500")
	}
}

func TestFetchCurrentValue_UnexpectedResultType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return matrix instead of vector
		body := promMatrixResponse([][]interface{}{{1609459200.0, "1.0"}})
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	_, _, err := c.FetchCurrentValue(context.Background(), "q")
	if err == nil {
		t.Error("expected error for non-vector result type")
	}
}

// ── FetchErrorRate ────────────────────────────────────────────────────────────

func TestFetchErrorRate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prometheus client sends query as POST form body
		if q := r.FormValue("query"); q == "" {
			t.Errorf("expected non-empty query in request body")
		}
		w.Header().Set("Content-Type", "application/json")
		body := promMatrixResponse([][]interface{}{
			{1609459200.0, "0.05"},
		})
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	points, err := c.FetchErrorRate(context.Background(), "checkout-api", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(points) != 1 {
		t.Errorf("expected 1 point, got %d", len(points))
	}
	if points[0].Value != 0.05 {
		t.Errorf("expected value 0.05, got %v", points[0].Value)
	}
}

func TestFetchErrorRate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c, _ := NewMetricsCollector(srv.URL)
	_, err := c.FetchErrorRate(context.Background(), "api", time.Minute)
	if err == nil {
		t.Error("expected error for server error")
	}
}
