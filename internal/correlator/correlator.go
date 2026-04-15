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
	"fmt"
	"strings"
	"time"

	"github.com/wachd/wachd/internal/collector"
)

// Context represents collected context for an incident
type Context struct {
	Commits []collector.Commit      `json:"commits"`
	Logs    []collector.LogLine     `json:"logs"`
	Metrics []collector.MetricPoint `json:"metrics"`
}

// Timeline represents a correlated timeline of events
type Timeline struct {
	AlertTime      time.Time       `json:"alert_time"`
	LastDeployTime *time.Time      `json:"last_deploy_time,omitempty"`
	LastCommit     *collector.Commit `json:"last_commit,omitempty"`
	ErrorSpike     *time.Time      `json:"error_spike,omitempty"`
	MetricAnomaly  *time.Time      `json:"metric_anomaly,omitempty"`
	Summary        string          `json:"summary"`
	Correlations   []string        `json:"correlations"`
}

// Correlator builds timelines from collected context
type Correlator struct{}

// NewCorrelator creates a new correlator
func NewCorrelator() *Correlator {
	return &Correlator{}
}

// BuildTimeline analyzes context and builds a timeline of events
func (c *Correlator) BuildTimeline(alertTime time.Time, ctx *Context) *Timeline {
	timeline := &Timeline{
		AlertTime:    alertTime,
		Correlations: []string{},
	}

	// Find last deploy (most recent commit)
	if len(ctx.Commits) > 0 {
		lastCommit := &ctx.Commits[0]
		timeline.LastCommit = lastCommit
		timeline.LastDeployTime = &lastCommit.Timestamp

		// Check if deploy was recent (within 30 minutes of alert)
		deployAge := alertTime.Sub(lastCommit.Timestamp)
		if deployAge < 30*time.Minute {
			correlation := fmt.Sprintf("Alert fired %s after recent deploy (commit %s by %s)",
				formatDuration(deployAge),
				lastCommit.SHA[:7],
				lastCommit.Author,
			)
			timeline.Correlations = append(timeline.Correlations, correlation)
		}
	}

	// Find error spike in logs
	if len(ctx.Logs) > 0 {
		firstError := ctx.Logs[0].Timestamp
		timeline.ErrorSpike = &firstError

		errorAge := alertTime.Sub(firstError)
		if errorAge < 10*time.Minute {
			correlation := fmt.Sprintf("Error logs started appearing %s before alert",
				formatDuration(errorAge),
			)
			timeline.Correlations = append(timeline.Correlations, correlation)
		}

		// Count error types
		errorCounts := c.countErrorTypes(ctx.Logs)
		if len(errorCounts) > 0 {
			topError := c.getMostCommonError(errorCounts)
			correlation := fmt.Sprintf("Most common error pattern: %s (%d occurrences)",
				topError.pattern,
				topError.count,
			)
			timeline.Correlations = append(timeline.Correlations, correlation)
		}
	}

	// Find metric anomalies
	if len(ctx.Metrics) > 0 {
		anomaly := c.findMetricAnomaly(ctx.Metrics, alertTime)
		if anomaly != nil {
			timeline.MetricAnomaly = anomaly
			anomalyAge := alertTime.Sub(*anomaly)
			correlation := fmt.Sprintf("Metric anomaly detected %s before alert",
				formatDuration(anomalyAge),
			)
			timeline.Correlations = append(timeline.Correlations, correlation)
		}
	}

	// Build summary
	timeline.Summary = c.buildSummary(timeline)

	return timeline
}

// countErrorTypes counts different error patterns in logs
func (c *Correlator) countErrorTypes(logs []collector.LogLine) map[string]int {
	counts := make(map[string]int)

	for _, log := range logs {
		// Extract error type/pattern (simplified)
		pattern := c.extractErrorPattern(log.Message)
		if pattern != "" {
			counts[pattern]++
		}
	}

	return counts
}

// extractErrorPattern extracts a normalized error pattern from a log message
func (c *Correlator) extractErrorPattern(message string) string {
	message = strings.ToLower(message)

	patterns := []string{
		"connection refused",
		"timeout",
		"out of memory",
		"null pointer",
		"database",
		"authentication failed",
		"permission denied",
		"not found",
		"internal server error",
	}

	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return pattern
		}
	}

	return ""
}

// getMostCommonError returns the most common error pattern
func (c *Correlator) getMostCommonError(counts map[string]int) struct{ pattern string; count int } {
	maxCount := 0
	maxPattern := ""

	for pattern, count := range counts {
		if count > maxCount {
			maxCount = count
			maxPattern = pattern
		}
	}

	return struct{ pattern string; count int }{maxPattern, maxCount}
}

// findMetricAnomaly finds significant metric spikes/drops
func (c *Correlator) findMetricAnomaly(metrics []collector.MetricPoint, alertTime time.Time) *time.Time {
	if len(metrics) < 2 {
		return nil
	}

	// Calculate baseline (average of first half)
	baseline := 0.0
	halfPoint := len(metrics) / 2
	for i := 0; i < halfPoint; i++ {
		baseline += metrics[i].Value
	}
	baseline /= float64(halfPoint)

	// Find significant deviations in second half
	threshold := baseline * 1.5 // 50% increase
	for i := halfPoint; i < len(metrics); i++ {
		if metrics[i].Value > threshold {
			return &metrics[i].Timestamp
		}
	}

	return nil
}

// buildSummary builds a human-readable summary
func (c *Correlator) buildSummary(timeline *Timeline) string {
	if len(timeline.Correlations) == 0 {
		return "Alert fired with no obvious correlations to recent events."
	}

	return fmt.Sprintf("Timeline analysis found %d correlations: %s",
		len(timeline.Correlations),
		strings.Join(timeline.Correlations, "; "),
	)
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f hours", d.Hours())
}
