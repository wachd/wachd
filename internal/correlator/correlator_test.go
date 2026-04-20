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

package correlator

import (
	"strings"
	"testing"
	"time"

	"github.com/wachd/wachd/internal/collector"
)

func TestNewCorrelator(t *testing.T) {
	c := NewCorrelator()
	if c == nil {
		t.Fatal("expected non-nil correlator")
	}
}

func TestBuildTimeline_Empty(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()

	timeline := c.BuildTimeline(now, &Context{})

	if timeline.AlertTime != now {
		t.Error("AlertTime not set correctly")
	}
	if timeline.LastCommit != nil {
		t.Error("expected no LastCommit")
	}
	if timeline.ErrorSpike != nil {
		t.Error("expected no ErrorSpike")
	}
	if timeline.MetricAnomaly != nil {
		t.Error("expected no MetricAnomaly")
	}
	if len(timeline.Correlations) != 0 {
		t.Errorf("expected no correlations, got %d", len(timeline.Correlations))
	}
	if !strings.Contains(timeline.Summary, "no obvious correlations") {
		t.Errorf("unexpected summary: %q", timeline.Summary)
	}
}

func TestBuildTimeline_RecentDeploy(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	deployTime := now.Add(-10 * time.Minute)

	ctx := &Context{
		Commits: []collector.Commit{
			{SHA: "abc1234def5678", Author: "john", Timestamp: deployTime, Message: "fix: bug fix"},
		},
	}

	timeline := c.BuildTimeline(now, ctx)

	if timeline.LastCommit == nil {
		t.Fatal("expected LastCommit to be set")
	}
	if timeline.LastDeployTime == nil {
		t.Fatal("expected LastDeployTime to be set")
	}

	found := false
	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "recent deploy") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'recent deploy' correlation, got: %v", timeline.Correlations)
	}
}

func TestBuildTimeline_OldDeploy_NoCorrelation(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	deployTime := now.Add(-2 * time.Hour)

	ctx := &Context{
		Commits: []collector.Commit{
			{SHA: "abc1234def5678", Author: "jane", Timestamp: deployTime},
		},
	}

	timeline := c.BuildTimeline(now, ctx)

	if timeline.LastCommit == nil {
		t.Error("expected LastCommit to be set")
	}

	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "recent deploy") {
			t.Errorf("unexpected recent-deploy correlation for old deploy: %s", corr)
		}
	}
}

func TestBuildTimeline_RecentErrorLogs(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	errorTime := now.Add(-5 * time.Minute)

	ctx := &Context{
		Logs: []collector.LogLine{
			{Timestamp: errorTime, Message: "connection refused to database", Level: "ERROR"},
			{Timestamp: errorTime.Add(time.Second), Message: "connection refused again", Level: "ERROR"},
			{Timestamp: errorTime.Add(2 * time.Second), Message: "timeout waiting for response", Level: "ERROR"},
		},
	}

	timeline := c.BuildTimeline(now, ctx)

	if timeline.ErrorSpike == nil {
		t.Error("expected ErrorSpike to be set")
	}

	foundLog := false
	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "Error logs") {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Errorf("expected error-log correlation, got: %v", timeline.Correlations)
	}

	foundPattern := false
	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "connection refused") {
			foundPattern = true
			break
		}
	}
	if !foundPattern {
		t.Errorf("expected error-pattern correlation, got: %v", timeline.Correlations)
	}
}

func TestBuildTimeline_OldErrorLogs_NoBeforeAlert(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	errorTime := now.Add(-20 * time.Minute)

	ctx := &Context{
		Logs: []collector.LogLine{
			{Timestamp: errorTime, Message: "connection refused", Level: "ERROR"},
		},
	}

	timeline := c.BuildTimeline(now, ctx)

	if timeline.ErrorSpike == nil {
		t.Error("expected ErrorSpike to be set even for old errors")
	}

	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "Error logs started appearing") {
			t.Errorf("unexpected 'before alert' correlation for old error: %s", corr)
		}
	}
}

func TestBuildTimeline_MetricAnomaly(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	baseTime := now.Add(-30 * time.Minute)

	var metrics []collector.MetricPoint
	for i := 0; i < 10; i++ {
		metrics = append(metrics, collector.MetricPoint{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Value:     10.0,
		})
	}
	// Spike to 20 — above 1.5x threshold of 10
	for i := 10; i < 20; i++ {
		metrics = append(metrics, collector.MetricPoint{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Value:     20.0,
		})
	}

	ctx := &Context{Metrics: metrics}
	timeline := c.BuildTimeline(now, ctx)

	if timeline.MetricAnomaly == nil {
		t.Error("expected MetricAnomaly to be detected")
	}

	found := false
	for _, corr := range timeline.Correlations {
		if strings.Contains(corr, "Metric anomaly") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected metric-anomaly correlation, got: %v", timeline.Correlations)
	}
}

func TestBuildTimeline_FlatMetrics_NoAnomaly(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	baseTime := now.Add(-30 * time.Minute)

	var metrics []collector.MetricPoint
	for i := 0; i < 20; i++ {
		metrics = append(metrics, collector.MetricPoint{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Value:     10.0,
		})
	}

	ctx := &Context{Metrics: metrics}
	timeline := c.BuildTimeline(now, ctx)

	if timeline.MetricAnomaly != nil {
		t.Error("did not expect MetricAnomaly for flat metrics")
	}
}

func TestBuildTimeline_SingleMetricPoint_NoAnomaly(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()

	ctx := &Context{
		Metrics: []collector.MetricPoint{
			{Timestamp: now, Value: 100.0},
		},
	}

	timeline := c.BuildTimeline(now, ctx)
	if timeline.MetricAnomaly != nil {
		t.Error("did not expect MetricAnomaly for single metric point")
	}
}

func TestBuildTimeline_Summary_WithMultipleCorrelations(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	deployTime := now.Add(-5 * time.Minute)
	errorTime := now.Add(-3 * time.Minute)

	ctx := &Context{
		Commits: []collector.Commit{
			{SHA: "abc1234def5678", Author: "alice", Timestamp: deployTime},
		},
		Logs: []collector.LogLine{
			{Timestamp: errorTime, Message: "timeout", Level: "ERROR"},
		},
	}

	timeline := c.BuildTimeline(now, ctx)
	if !strings.Contains(timeline.Summary, "Timeline analysis found") {
		t.Errorf("expected 'Timeline analysis found' in summary, got: %q", timeline.Summary)
	}
}

// ── extractErrorPattern ──────────────────────────────────────────────────────

func TestExtractErrorPattern(t *testing.T) {
	c := NewCorrelator()

	tests := []struct {
		message  string
		expected string
	}{
		{"Connection Refused to server", "connection refused"},
		{"Request TIMEOUT after 30s", "timeout"},
		{"Out Of Memory error", "out of memory"},
		{"Null Pointer Exception at line 42", "null pointer"},
		{"Database connection failed", "database"},
		{"Authentication Failed for user", "authentication failed"},
		{"Permission Denied accessing file", "permission denied"},
		{"Resource Not Found: /api/v1/users", "not found"},
		{"Internal Server Error 500", "internal server error"},
		{"some random log line", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := c.extractErrorPattern(tt.message)
		if result != tt.expected {
			t.Errorf("extractErrorPattern(%q) = %q, want %q", tt.message, result, tt.expected)
		}
	}
}

// ── countErrorTypes ──────────────────────────────────────────────────────────

func TestCountErrorTypes(t *testing.T) {
	c := NewCorrelator()

	logs := []collector.LogLine{
		{Message: "connection refused to postgres"},
		{Message: "connection refused to redis"},
		{Message: "timeout waiting for response"},
		{Message: "some unrecognized message"},
	}

	counts := c.countErrorTypes(logs)

	if counts["connection refused"] != 2 {
		t.Errorf("expected 2 connection refused, got %d", counts["connection refused"])
	}
	if counts["timeout"] != 1 {
		t.Errorf("expected 1 timeout, got %d", counts["timeout"])
	}
	if len(counts) != 2 {
		t.Errorf("expected 2 unique error types, got %d", len(counts))
	}
}

func TestCountErrorTypes_Empty(t *testing.T) {
	c := NewCorrelator()
	counts := c.countErrorTypes(nil)
	if len(counts) != 0 {
		t.Errorf("expected empty counts, got %v", counts)
	}
}

// ── getMostCommonError ───────────────────────────────────────────────────────

func TestGetMostCommonError(t *testing.T) {
	c := NewCorrelator()

	counts := map[string]int{
		"timeout":           3,
		"connection refused": 7,
		"not found":         1,
	}

	result := c.getMostCommonError(counts)
	if result.pattern != "connection refused" {
		t.Errorf("expected 'connection refused', got %q", result.pattern)
	}
	if result.count != 7 {
		t.Errorf("expected count 7, got %d", result.count)
	}
}

func TestGetMostCommonError_SingleEntry(t *testing.T) {
	c := NewCorrelator()
	counts := map[string]int{"timeout": 5}
	result := c.getMostCommonError(counts)
	if result.count != 5 {
		t.Errorf("expected count 5, got %d", result.count)
	}
}

// ── findMetricAnomaly ────────────────────────────────────────────────────────

func TestFindMetricAnomaly_NilOrEmpty(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()

	if c.findMetricAnomaly(nil, now) != nil {
		t.Error("expected nil for nil metrics")
	}
	if c.findMetricAnomaly([]collector.MetricPoint{}, now) != nil {
		t.Error("expected nil for empty metrics")
	}
	if c.findMetricAnomaly([]collector.MetricPoint{{Timestamp: now, Value: 10}}, now) != nil {
		t.Error("expected nil for single metric point")
	}
}

func TestFindMetricAnomaly_BelowThreshold(t *testing.T) {
	c := NewCorrelator()
	now := time.Now()
	baseTime := now.Add(-20 * time.Minute)

	var metrics []collector.MetricPoint
	for i := 0; i < 10; i++ {
		metrics = append(metrics, collector.MetricPoint{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Value:     10.0,
		})
	}
	// Second half: 14.9 — below 1.5x threshold (15.0)
	for i := 10; i < 20; i++ {
		metrics = append(metrics, collector.MetricPoint{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Value:     14.9,
		})
	}

	if c.findMetricAnomaly(metrics, now) != nil {
		t.Error("expected no anomaly for value below 1.5x threshold")
	}
}

// ── formatDuration ───────────────────────────────────────────────────────────

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{5 * time.Second, "5 seconds"},
		{30 * time.Second, "30 seconds"},
		{59 * time.Second, "59 seconds"},
		{60 * time.Second, "1 minutes"},
		{5 * time.Minute, "5 minutes"},
		{59 * time.Minute, "59 minutes"},
		{time.Hour, "1.0 hours"},
		{2 * time.Hour, "2.0 hours"},
		{90 * time.Minute, "1.5 hours"},
	}

	for _, tt := range tests {
		result := formatDuration(tt.d)
		if result != tt.expected {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, result, tt.expected)
		}
	}
}
