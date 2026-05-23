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
	"errors"
	"testing"
	"time"

	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/store"
)

type fakeGrafanaMCPCollector struct {
	logs        []collector.LogLine
	metrics     []collector.MetricPoint
	logsErr     error
	metricsErr  error
	logsCalls   int
	metricCalls int
}

func (f *fakeGrafanaMCPCollector) FetchErrorLogs(context.Context, string, time.Time, time.Time, int) ([]collector.LogLine, error) {
	f.logsCalls++
	return f.logs, f.logsErr
}

func (f *fakeGrafanaMCPCollector) FetchErrorRate(context.Context, string, time.Duration) ([]collector.MetricPoint, error) {
	f.metricCalls++
	return f.metrics, f.metricsErr
}

func TestWorker_CollectGrafanaMCPContext_UsesMCPWhenConfigured(t *testing.T) {
	enc, err := auth.NewEncryptor("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	encrypted, err := enc.Encrypt("grafana-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	fake := &fakeGrafanaMCPCollector{
		logs:    []collector.LogLine{{Timestamp: time.Now().UTC(), Message: "fatal", Labels: map[string]string{"service": "api"}}},
		metrics: []collector.MetricPoint{{Timestamp: time.Now().UTC(), Value: 2.5}},
	}
	originalFactory := newGrafanaMCPCollector
	newGrafanaMCPCollector = func(endpoint, token string) grafanaMCPCollector {
		if endpoint != "https://grafana.com/mcp" {
			t.Fatalf("unexpected endpoint %q", endpoint)
		}
		if token != "grafana-token" {
			t.Fatalf("unexpected decrypted token %q", token)
		}
		return fake
	}
	defer func() { newGrafanaMCPCollector = originalFactory }()

	w := &Worker{enc: enc}
	result := &correlator.Context{}
	url := "https://grafana.com/mcp"
	cfg := &store.TeamConfig{GrafanaMCPURL: &url, GrafanaMCPTokenEncrypted: &encrypted}

	logsSuccess, metricsSuccess := w.collectGrafanaMCPContext(context.Background(), cfg, "api", time.Now().Add(-time.Hour), time.Now(), result)
	if !logsSuccess || !metricsSuccess {
		t.Fatalf("expected Grafana MCP collection success, got logs=%v metrics=%v", logsSuccess, metricsSuccess)
	}
	if len(result.Logs) != 1 || len(result.Metrics) != 1 {
		t.Fatalf("expected logs and metrics to be populated, got %+v", result)
	}
}

func TestWorker_CollectGrafanaMCPContext_FallsBackOnError(t *testing.T) {
	enc, err := auth.NewEncryptor("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	encrypted, _ := enc.Encrypt("grafana-token")
	fake := &fakeGrafanaMCPCollector{logsErr: errors.New("boom"), metricsErr: errors.New("boom")}
	originalFactory := newGrafanaMCPCollector
	newGrafanaMCPCollector = func(string, string) grafanaMCPCollector { return fake }
	defer func() { newGrafanaMCPCollector = originalFactory }()

	w := &Worker{enc: enc}
	result := &correlator.Context{}
	url := "https://grafana.com/mcp"
	cfg := &store.TeamConfig{GrafanaMCPURL: &url, GrafanaMCPTokenEncrypted: &encrypted}

	logsSuccess, metricsSuccess := w.collectGrafanaMCPContext(context.Background(), cfg, "api", time.Now().Add(-time.Hour), time.Now(), result)
	if logsSuccess || metricsSuccess {
		t.Fatalf("expected MCP errors to trigger fallback flags, got logs=%v metrics=%v", logsSuccess, metricsSuccess)
	}
	if len(result.Logs) != 0 || len(result.Metrics) != 0 {
		t.Fatalf("expected no Grafana MCP data on errors, got %+v", result)
	}
}
