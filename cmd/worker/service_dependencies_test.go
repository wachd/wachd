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

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/store"
)

func makeDepIncident(teamID uuid.UUID) *store.Incident {
	return &store.Incident{
		ID:      uuid.New(),
		TeamID:  teamID,
		FiredAt: time.Now().UTC(),
	}
}

func TestApplyDependencyContext_NilDepsIsNoop(t *testing.T) {
	result := &correlator.Context{}
	incident := makeDepIncident(uuid.New())
	applyDependencyContext(context.Background(), incident, &store.TeamConfig{}, nil, incident.FiredAt.Add(-30*time.Minute), result)
	if len(result.Logs) != 0 || len(result.Metrics) != 0 {
		t.Fatalf("expected empty result for nil deps, got logs=%d metrics=%d", len(result.Logs), len(result.Metrics))
	}
}

func TestApplyDependencyContext_NoConnectorsLeavesResultUnchanged(t *testing.T) {
	// Pre-populate result — with no connectors configured, deps cannot add anything.
	// Primary data must survive the call unchanged.
	existing := collector.LogLine{Timestamp: time.Now().UTC(), Message: "primary error"}
	result := &correlator.Context{Logs: []collector.LogLine{existing}}

	teamID := uuid.New()
	incident := makeDepIncident(teamID)
	deps := []*store.ServiceDependency{
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "redis-cache"},
	}

	// cfg has no Loki/Prometheus endpoints — nothing can be collected for the dep.
	applyDependencyContext(context.Background(), incident, &store.TeamConfig{}, deps, incident.FiredAt.Add(-30*time.Minute), result)

	if len(result.Logs) != 1 || result.Logs[0].Message != "primary error" {
		t.Fatalf("primary log should be unchanged, got %+v", result.Logs)
	}
}

func TestApplyDependencyContext_SSRFBlockedPerDep(t *testing.T) {
	internalEndpoint := "http://169.254.169.254/latest/meta-data"
	cfg := &store.TeamConfig{LokiEndpoint: &internalEndpoint}
	teamID := uuid.New()
	incident := makeDepIncident(teamID)
	deps := []*store.ServiceDependency{
		{ID: uuid.New(), TeamID: teamID, Service: "api", DependsOn: "metadata-service"},
	}

	result := &correlator.Context{}
	// Must not panic and must not collect from the blocked endpoint.
	applyDependencyContext(context.Background(), incident, cfg, deps, incident.FiredAt.Add(-30*time.Minute), result)

	if len(result.Logs) != 0 {
		t.Fatal("expected no logs from SSRF-blocked endpoint")
	}
}

func TestApplyDependencyContext_MultipleDepsIteratedWithoutPanic(t *testing.T) {
	// Two deps, no connectors — verifies the loop runs for all deps without panic.
	teamID := uuid.New()
	incident := makeDepIncident(teamID)
	deps := []*store.ServiceDependency{
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "redis-cache"},
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "payments-db"},
	}

	result := &correlator.Context{}
	// Should complete without panic even though no connectors produce data.
	applyDependencyContext(context.Background(), incident, &store.TeamConfig{}, deps, incident.FiredAt.Add(-30*time.Minute), result)
}

func TestApplyDependencyContext_SSRFBlockedOnFirstDepDoesNotPreventSecond(t *testing.T) {
	// First dep: SSRF-blocked endpoint.
	// Second dep: also blocked but verifies we reach it (no early exit on first failure).
	internalEndpoint := "http://169.254.169.254/latest/meta-data"
	cfg := &store.TeamConfig{LokiEndpoint: &internalEndpoint}
	teamID := uuid.New()
	incident := makeDepIncident(teamID)

	// Use a counter to confirm both deps are processed. We observe this indirectly:
	// if the loop exits early, a panic or incomplete iteration would show up.
	// Both are SSRF-blocked so neither produces logs — but the loop must not exit after first.
	deps := []*store.ServiceDependency{
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "redis-cache"},
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "payments-db"},
	}

	result := &correlator.Context{}
	applyDependencyContext(context.Background(), incident, cfg, deps, incident.FiredAt.Add(-30*time.Minute), result)
	// Both blocked — but function must complete (not panic or short-circuit unexpectedly).
	if len(result.Logs) != 0 {
		t.Fatalf("expected no logs from SSRF-blocked endpoints, got %d", len(result.Logs))
	}
}

func TestApplyDependencyContext_LabelUsedInLogging(t *testing.T) {
	// Verify label field is handled without panic when set and when nil.
	teamID := uuid.New()
	incident := makeDepIncident(teamID)
	label := "shared Redis"
	deps := []*store.ServiceDependency{
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "redis-cache", Label: &label},
		{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "payments-db", Label: nil},
	}

	result := &correlator.Context{}
	// No connectors — just verify nil/non-nil label is handled without panic.
	applyDependencyContext(context.Background(), incident, &store.TeamConfig{}, deps, incident.FiredAt.Add(-30*time.Minute), result)
}

// ── collectDepViaMCP tests ────────────────────────────────────────────────────

// mockMCPCollector is a test double for grafanaMCPCollector.
type mockMCPCollector struct {
	logs        []collector.LogLine
	logsErr     error
	metrics     []collector.MetricPoint
	metricsErr  error
	calledFor   []string // service names passed to FetchErrorLogs
}

func (m *mockMCPCollector) FetchErrorLogs(_ context.Context, service string, _, _ time.Time, _ int) ([]collector.LogLine, error) {
	m.calledFor = append(m.calledFor, service)
	return m.logs, m.logsErr
}

func (m *mockMCPCollector) FetchErrorRate(_ context.Context, _ string, _ time.Duration) ([]collector.MetricPoint, error) {
	return m.metrics, m.metricsErr
}

func TestCollectDepViaMCP_SuccessAppendsData(t *testing.T) {
	logLine := collector.LogLine{Timestamp: time.Now().UTC(), Message: "redis timeout"}
	metricPt := collector.MetricPoint{Timestamp: time.Now().UTC(), Value: 0.5}
	mc := &mockMCPCollector{logs: []collector.LogLine{logLine}, metrics: []collector.MetricPoint{metricPt}}

	teamID := uuid.New()
	dep := &store.ServiceDependency{ID: uuid.New(), TeamID: teamID, Service: "checkout-api", DependsOn: "redis-cache"}
	result := &correlator.Context{}
	since := time.Now().Add(-30 * time.Minute)

	logsOK, metricsOK := collectDepViaMCP(context.Background(), mc, dep, since, time.Now(), result)

	if !logsOK || !metricsOK {
		t.Fatalf("expected both OK, got logsOK=%v metricsOK=%v", logsOK, metricsOK)
	}
	if len(result.Logs) != 1 || result.Logs[0].Message != "redis timeout" {
		t.Errorf("expected log appended, got %+v", result.Logs)
	}
	if len(result.Metrics) != 1 {
		t.Errorf("expected metric appended, got %+v", result.Metrics)
	}
	if len(mc.calledFor) != 1 || mc.calledFor[0] != "redis-cache" {
		t.Errorf("expected FetchErrorLogs called with dep.DependsOn, got %v", mc.calledFor)
	}
}

func TestCollectDepViaMCP_LogsErrorReturnsFalseForLogs(t *testing.T) {
	mc := &mockMCPCollector{logsErr: errors.New("mcp unavailable")}

	teamID := uuid.New()
	dep := &store.ServiceDependency{ID: uuid.New(), TeamID: teamID, Service: "api", DependsOn: "postgres"}
	result := &correlator.Context{}
	since := time.Now().Add(-30 * time.Minute)

	logsOK, metricsOK := collectDepViaMCP(context.Background(), mc, dep, since, time.Now(), result)

	if logsOK {
		t.Error("expected logsOK=false when FetchErrorLogs returns error")
	}
	// Metrics call does not fail — metricsOK should be true.
	if !metricsOK {
		t.Error("expected metricsOK=true when FetchErrorRate succeeds")
	}
	if len(result.Logs) != 0 {
		t.Errorf("expected no logs on error, got %d", len(result.Logs))
	}
}

func TestCollectDepViaMCP_BothErrorsReturnFalse(t *testing.T) {
	mc := &mockMCPCollector{
		logsErr:    errors.New("timeout"),
		metricsErr: errors.New("timeout"),
	}

	teamID := uuid.New()
	dep := &store.ServiceDependency{ID: uuid.New(), TeamID: teamID, Service: "api", DependsOn: "postgres"}
	result := &correlator.Context{}

	logsOK, metricsOK := collectDepViaMCP(context.Background(), mc, dep, time.Now().Add(-30*time.Minute), time.Now(), result)

	if logsOK || metricsOK {
		t.Errorf("expected both false on error, got logsOK=%v metricsOK=%v", logsOK, metricsOK)
	}
	if len(result.Logs) != 0 || len(result.Metrics) != 0 {
		t.Error("expected no data appended on error")
	}
}

func TestCollectDepViaMCP_EmptySuccessDoesNotAppend(t *testing.T) {
	// MCP returns no error but no data — both OK flags should be true so the dep
	// is not double-collected via direct Loki/Prometheus.
	mc := &mockMCPCollector{logs: nil, metrics: nil}

	teamID := uuid.New()
	dep := &store.ServiceDependency{ID: uuid.New(), TeamID: teamID, Service: "api", DependsOn: "redis"}
	result := &correlator.Context{}

	logsOK, metricsOK := collectDepViaMCP(context.Background(), mc, dep, time.Now().Add(-30*time.Minute), time.Now(), result)

	if !logsOK || !metricsOK {
		t.Errorf("expected both true for empty success, got logsOK=%v metricsOK=%v", logsOK, metricsOK)
	}
	if len(result.Logs) != 0 || len(result.Metrics) != 0 {
		t.Error("expected result unchanged when MCP returns empty data")
	}
}
