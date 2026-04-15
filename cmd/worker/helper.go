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
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/store"
)

// collectContext gathers context from the team's configured data sources.
// Config is loaded from the database per team — not from global env vars.
func (w *Worker) collectContext(ctx context.Context, incident *store.Incident) *correlator.Context {
	result := &correlator.Context{
		Commits: []collector.Commit{},
		Logs:    []collector.LogLine{},
		Metrics: []collector.MetricPoint{},
	}

	cfg, err := w.db.GetTeamConfig(ctx, incident.TeamID)
	if err != nil {
		log.Printf("  ⚠ Failed to load team config: %v", err)
		return result
	}
	if cfg == nil {
		log.Printf("  ⚠ No team config for team %s — skipping context collection", incident.TeamID)
		return result
	}

	serviceName := w.extractServiceName(incident)
	if serviceName == "" {
		log.Printf("  ⚠ Cannot determine service name from alert — skipping targeted collection")
		return result
	}
	log.Printf("  Service: %s", serviceName)

	since := incident.FiredAt.Add(-30 * time.Minute)

	// GitHub commits
	if cfg.GitHubTokenEncrypted != nil && w.enc != nil {
		token, err := w.enc.Decrypt(*cfg.GitHubTokenEncrypted)
		if err != nil {
			log.Printf("  ⚠ Failed to decrypt GitHub token: %v", err)
		} else if token != "" {
			var repos []string
			if cfg.GitHubRepos != nil {
				if err := json.Unmarshal(cfg.GitHubRepos, &repos); err != nil {
					log.Printf("  ⚠ Failed to parse github_repos: %v", err)
				}
			}
			if len(repos) > 0 {
				gc := collector.NewGitCollector(token)
				for _, repo := range repos {
					commits, err := gc.FetchRecentCommits(ctx, repo, "main", since, 10)
					if err != nil {
						log.Printf("  ⚠ GitHub %s: %v", repo, err)
						continue
					}
					result.Commits = append(result.Commits, commits...)
					if len(commits) > 0 {
						log.Printf("  ✓ %d commits from %s", len(commits), repo)
					}
				}
			}
		}
	}

	// Loki logs
	if cfg.LokiEndpoint != nil && *cfg.LokiEndpoint != "" {
		lc := collector.NewLogsCollector(*cfg.LokiEndpoint)
		logs, err := lc.FetchErrorLogs(ctx, serviceName, since, incident.FiredAt, 50)
		if err != nil {
			log.Printf("  ⚠ Loki: %v", err)
		} else if len(logs) > 0 {
			result.Logs = logs
			log.Printf("  ✓ %d error logs from Loki", len(logs))
		}
	}

	// Prometheus metrics
	if cfg.PrometheusEndpoint != nil && *cfg.PrometheusEndpoint != "" {
		mc, err := collector.NewMetricsCollector(*cfg.PrometheusEndpoint)
		if err != nil {
			log.Printf("  ⚠ Prometheus client: %v", err)
		} else {
			metrics, err := mc.FetchErrorRate(ctx, serviceName, 30*time.Minute)
			if err != nil {
				log.Printf("  ⚠ Prometheus query: %v", err)
			} else if len(metrics) > 0 {
				result.Metrics = metrics
				log.Printf("  ✓ %d metric points from Prometheus", len(metrics))
			}
		}
	}

	return result
}

// sanitizeContext strips PII from all collected context before it touches the AI engine.
func (w *Worker) sanitizeContext(ctx *correlator.Context) *correlator.Context {
	sanitized := &correlator.Context{
		Commits: make([]collector.Commit, len(ctx.Commits)),
		Logs:    make([]collector.LogLine, len(ctx.Logs)),
		Metrics: ctx.Metrics, // numeric only — no PII
	}
	for i, c := range ctx.Commits {
		sanitized.Commits[i] = c
		sanitized.Commits[i].Message = w.sanitiser.Sanitise(c.Message)
		sanitized.Commits[i].Author = w.sanitiser.Sanitise(c.Author)
	}
	for i, l := range ctx.Logs {
		sanitized.Logs[i] = l
		sanitized.Logs[i].Message = w.sanitiser.Sanitise(l.Message)
		sanitized.Logs[i].Labels = w.sanitiser.SanitiseMap(l.Labels)
	}
	return sanitized
}

// updateIncidentContext persists collected context and correlation timeline to the DB.
func (w *Worker) updateIncidentContext(ctx context.Context, incident *store.Incident, collectedCtx *correlator.Context, timeline *correlator.Timeline) error {
	ctxJSON, err := json.Marshal(collectedCtx)
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}
	timelineJSON, err := json.Marshal(timeline)
	if err != nil {
		return fmt.Errorf("marshal timeline: %w", err)
	}

	_, err = w.db.Pool().Exec(ctx, `
		UPDATE incidents
		SET context = $1, analysis = $2, updated_at = $3
		WHERE id = $4 AND team_id = $5
	`, ctxJSON, timelineJSON, time.Now(), incident.ID, incident.TeamID)
	return err
}

// extractServiceName attempts to determine the affected service from the alert payload.
func (w *Worker) extractServiceName(incident *store.Incident) string {
	var payload map[string]interface{}
	if err := json.Unmarshal(incident.AlertPayload, &payload); err != nil {
		return ""
	}
	if tags, ok := payload["tags"].(map[string]interface{}); ok {
		if svc, ok := tags["service"].(string); ok && svc != "" {
			return svc
		}
	}
	// Fallback: "High CPU — web-server" → "web-server"
	if strings.Contains(incident.Title, " — ") {
		parts := strings.SplitN(incident.Title, " — ", 2)
		return strings.TrimSpace(parts[1])
	}
	if strings.Contains(incident.Title, " - ") {
		parts := strings.SplitN(incident.Title, " - ", 2)
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// updateIncidentAnalysis overwrites the analysis JSONB field with the AI result.
func (w *Worker) updateIncidentAnalysis(ctx context.Context, incident *store.Incident, analysis *ai.AnalysisResponse) error {
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		return fmt.Errorf("marshal analysis: %w", err)
	}
	_, err = w.db.Pool().Exec(ctx, `
		UPDATE incidents SET analysis = $1, updated_at = $2 WHERE id = $3 AND team_id = $4
	`, analysisJSON, time.Now(), incident.ID, incident.TeamID)
	return err
}

func formatMetricsSummary(metrics []collector.MetricPoint) string {
	if len(metrics) == 0 {
		return "No metrics available"
	}
	sum, min, max := 0.0, metrics[0].Value, metrics[0].Value
	for _, m := range metrics {
		sum += m.Value
		if m.Value < min {
			min = m.Value
		}
		if m.Value > max {
			max = m.Value
		}
	}
	return fmt.Sprintf("avg=%.2f, min=%.2f, max=%.2f over %d points", sum/float64(len(metrics)), min, max, len(metrics))
}

func formatTimeline(timeline *correlator.Timeline) string {
	if len(timeline.Correlations) == 0 {
		return "No correlations detected"
	}
	return strings.Join(timeline.Correlations, "; ")
}
