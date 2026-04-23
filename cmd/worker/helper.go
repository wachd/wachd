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

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/oncall"
	"github.com/wachd/wachd/internal/store"
	"github.com/wachd/wachd/internal/validate"
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
		if err := validate.EndpointURL(*cfg.LokiEndpoint); err != nil {
			log.Printf("  ⚠ Loki endpoint blocked (SSRF): %v", err)
		} else {
			lc := collector.NewLogsCollector(*cfg.LokiEndpoint)
			logs, err := lc.FetchErrorLogs(ctx, serviceName, since, incident.FiredAt, 50)
			if err != nil {
				log.Printf("  ⚠ Loki: %v", err)
			} else if len(logs) > 0 {
				result.Logs = logs
				log.Printf("  ✓ %d error logs from Loki", len(logs))
			}
		}
	}

	// Prometheus metrics
	if cfg.PrometheusEndpoint != nil && *cfg.PrometheusEndpoint != "" {
		if err := validate.EndpointURL(*cfg.PrometheusEndpoint); err != nil {
			log.Printf("  ⚠ Prometheus endpoint blocked (SSRF): %v", err)
		} else {
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
	}

	// Dynatrace — problems, logs, and metrics
	if cfg.DynatraceEndpoint != nil && *cfg.DynatraceEndpoint != "" && cfg.DynatraceTokenEncrypted != nil && w.enc != nil {
		if err := validate.EndpointURL(*cfg.DynatraceEndpoint); err != nil {
			log.Printf("  ⚠ Dynatrace endpoint blocked (SSRF): %v", err)
		} else {
			dtToken, err := w.enc.Decrypt(*cfg.DynatraceTokenEncrypted)
			if err != nil {
				log.Printf("  ⚠ Failed to decrypt Dynatrace token: %v", err)
			} else if dtToken != "" {
				dc := collector.NewDynatraceCollector(*cfg.DynatraceEndpoint, dtToken)

				// Problems (Dynatrace pre-correlated anomalies)
				problems, err := dc.FetchProblems(ctx, serviceName, since, 10)
				if err != nil {
					log.Printf("  ⚠ Dynatrace problems: %v", err)
				} else if len(problems) > 0 {
					for _, p := range problems {
						result.Logs = append(result.Logs, collector.LogLine{
							Timestamp: p.StartTime,
							Message:   "[Dynatrace Problem] " + p.Title + " (severity: " + p.Severity + ", status: " + p.Status + ")",
							Level:     p.Severity,
							Labels:    map[string]string{"source": "dynatrace", "problem_id": p.ID},
						})
					}
					log.Printf("  ✓ %d problems from Dynatrace", len(problems))
				}

				// Error logs
				dtLogs, err := dc.FetchLogs(ctx, serviceName, since, incident.FiredAt, 50)
				if err != nil {
					log.Printf("  ⚠ Dynatrace logs: %v", err)
				} else if len(dtLogs) > 0 {
					result.Logs = append(result.Logs, dtLogs...)
					log.Printf("  ✓ %d log lines from Dynatrace", len(dtLogs))
				}

				// Error rate metric
				dtMetrics, err := dc.FetchMetrics(ctx, serviceName, "builtin:service.errors.total.rate", since, incident.FiredAt)
				if err != nil {
					log.Printf("  ⚠ Dynatrace metrics: %v", err)
				} else if len(dtMetrics) > 0 {
					result.Metrics = append(result.Metrics, dtMetrics...)
					log.Printf("  ✓ %d metric points from Dynatrace", len(dtMetrics))
				}
			}
		}
	}

	// Splunk — error logs and notable events
	if cfg.SplunkEndpoint != nil && *cfg.SplunkEndpoint != "" && cfg.SplunkTokenEncrypted != nil && w.enc != nil {
		if err := validate.EndpointURL(*cfg.SplunkEndpoint); err != nil {
			log.Printf("  ⚠ Splunk endpoint blocked (SSRF): %v", err)
		} else {
			splunkToken, err := w.enc.Decrypt(*cfg.SplunkTokenEncrypted)
			if err != nil {
				log.Printf("  ⚠ Failed to decrypt Splunk token: %v", err)
			} else if splunkToken != "" {
				sc := collector.NewSplunkCollector(*cfg.SplunkEndpoint, splunkToken)

				// Error logs via SPL
				splunkLogs, err := sc.FetchLogs(ctx, serviceName, since, incident.FiredAt, 50)
				if err != nil {
					log.Printf("  ⚠ Splunk logs: %v", err)
				} else if len(splunkLogs) > 0 {
					result.Logs = append(result.Logs, splunkLogs...)
					log.Printf("  ✓ %d log lines from Splunk", len(splunkLogs))
				}

				// Notable events (ITSI/ES)
				notables, err := sc.FetchNotableEvents(ctx, serviceName, since, 10)
				if err != nil {
					log.Printf("  ⚠ Splunk notable events: %v", err)
				} else if len(notables) > 0 {
					for _, n := range notables {
						result.Logs = append(result.Logs, collector.LogLine{
							Timestamp: n.Timestamp,
							Message:   "[Splunk Notable] " + n.Raw,
							Level:     "error",
							Labels:    n.Fields,
						})
					}
					log.Printf("  ✓ %d notable events from Splunk", len(notables))
				}
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
// Handles multiple alert source formats:
//   - Grafana legacy:          payload.tags.service
//   - Grafana unified alerting: payload.commonLabels.service / alerts[0].labels.service
//   - Prometheus Alertmanager:  payload.labels.service
//   - Kubernetes labels:        deployment / pod (with hash suffix stripped)
//   - Title fallback:           "High CPU — web-server" or "High CPU - web-server"
func (w *Worker) extractServiceName(incident *store.Incident) string {
	var payload map[string]interface{}
	if err := json.Unmarshal(incident.AlertPayload, &payload); err != nil {
		return ""
	}

	// Helper: extract string from a nested map
	strVal := func(m map[string]interface{}, key string) string {
		if v, ok := m[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}

	// Helper: strip k8s pod hash suffix — "demo-backend-5cc9f44fd6-66scm" → "demo-backend"
	stripPodSuffix := func(pod string) string {
		parts := strings.Split(pod, "-")
		if len(parts) >= 3 {
			// Last two segments are the ReplicaSet hash + pod hash (5 chars each)
			last := parts[len(parts)-1]
			secondLast := parts[len(parts)-2]
			if len(last) == 5 && len(secondLast) == 10 {
				return strings.Join(parts[:len(parts)-2], "-")
			}
		}
		return pod
	}

	// Candidates in priority order — first non-empty wins
	candidates := []func() string{
		// 1. Grafana legacy: tags.service
		func() string {
			if tags, ok := payload["tags"].(map[string]interface{}); ok {
				return strVal(tags, "service")
			}
			return ""
		},
		// 2. Grafana unified / Alertmanager: commonLabels.service
		func() string {
			if cl, ok := payload["commonLabels"].(map[string]interface{}); ok {
				return strVal(cl, "service")
			}
			return ""
		},
		// 3. Grafana unified: alerts[0].labels.service
		func() string {
			if alerts, ok := payload["alerts"].([]interface{}); ok && len(alerts) > 0 {
				if a, ok := alerts[0].(map[string]interface{}); ok {
					if labels, ok := a["labels"].(map[string]interface{}); ok {
						return strVal(labels, "service")
					}
				}
			}
			return ""
		},
		// 4. Prometheus Alertmanager: labels.service
		func() string {
			if labels, ok := payload["labels"].(map[string]interface{}); ok {
				return strVal(labels, "service")
			}
			return ""
		},
		// 5. Kubernetes deployment label (commonLabels or alerts[0].labels)
		func() string {
			for _, src := range []string{"commonLabels", "labels"} {
				if m, ok := payload[src].(map[string]interface{}); ok {
					if d := strVal(m, "deployment"); d != "" {
						return d
					}
				}
			}
			if alerts, ok := payload["alerts"].([]interface{}); ok && len(alerts) > 0 {
				if a, ok := alerts[0].(map[string]interface{}); ok {
					if labels, ok := a["labels"].(map[string]interface{}); ok {
						if d := strVal(labels, "deployment"); d != "" {
							return d
						}
					}
				}
			}
			return ""
		},
		// 6. Kubernetes pod label — strip hash suffix
		func() string {
			for _, src := range []string{"commonLabels", "labels"} {
				if m, ok := payload[src].(map[string]interface{}); ok {
					if p := strVal(m, "pod"); p != "" {
						return stripPodSuffix(p)
					}
				}
			}
			if alerts, ok := payload["alerts"].([]interface{}); ok && len(alerts) > 0 {
				if a, ok := alerts[0].(map[string]interface{}); ok {
					if labels, ok := a["labels"].(map[string]interface{}); ok {
						if p := strVal(labels, "pod"); p != "" {
							return stripPodSuffix(p)
						}
					}
				}
			}
			return ""
		},
		// 7. Grafana legacy: tags.app / tags.job
		func() string {
			if tags, ok := payload["tags"].(map[string]interface{}); ok {
				for _, key := range []string{"app", "job", "container"} {
					if v := strVal(tags, key); v != "" {
						return v
					}
				}
			}
			return ""
		},
		// 8. Title split: "High CPU — web-server" or "High CPU - web-server"
		func() string {
			for _, sep := range []string{" — ", " - "} {
				if strings.Contains(incident.Title, sep) {
					parts := strings.SplitN(incident.Title, sep, 2)
					return strings.TrimSpace(parts[1])
				}
			}
			return ""
		},
	}

	for _, fn := range candidates {
		if s := fn(); s != "" {
			return s
		}
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

// runEscalationLoop polls every 30 seconds for open incidents that have exceeded
// their next escalation threshold and notifies the appropriate layer.
func (w *Worker) runEscalationLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkEscalations(ctx)
		}
	}
}

func (w *Worker) checkEscalations(ctx context.Context) {
	// Minimum possible notify_after_minutes is 1 — only fetch incidents older than that.
	incidents, err := w.db.GetOpenIncidentsForEscalation(ctx, time.Minute)
	if err != nil {
		log.Printf("escalation: query failed: %v", err)
		return
	}
	for _, incident := range incidents {
		w.checkEscalationForIncident(ctx, incident)
	}
}

func (w *Worker) checkEscalationForIncident(ctx context.Context, incident *store.Incident) {
	policy, err := w.db.GetEscalationPolicy(ctx, incident.TeamID)
	if err != nil || policy == nil {
		return // no policy configured
	}

	var cfg oncall.EscalationConfig
	if err := json.Unmarshal(policy.Config, &cfg); err != nil {
		log.Printf("escalation: invalid policy for team %s: %v", incident.TeamID, err)
		return
	}

	numLayers := len(cfg.Layers)
	if numLayers == 0 {
		return
	}

	// escalation_step is 0-based and monotonically increasing across all repeats:
	//   steps 0..N-1  = initial cycle
	//   steps N..2N-1 = repeat 1
	//   steps 2N..    = repeat 2, etc.
	// Layer 0 was already notified during initial job processing (step 0).
	nextStep := incident.EscalationStep + 1
	nextRepeat := nextStep / numLayers
	nextLayerIdx := nextStep % numLayers

	// Stop if repeat limit reached (max_repeats=0 means repeat forever).
	if cfg.MaxRepeats > 0 && nextRepeat > cfg.MaxRepeats {
		return
	}
	// Stop if no repeat configured and the initial cycle is done.
	if cfg.RepeatIntervalMinutes == 0 && nextRepeat > 0 {
		return
	}

	nextLayer := cfg.Layers[nextLayerIdx]

	// Compute the absolute elapsed threshold for this step from incident.FiredAt.
	// cycleDuration = last layer's notify_after_minutes (time to complete one cycle).
	// Threshold for repeat R, layer K = (cycleDuration + repeatInterval) * R + layers[K].notify_after_minutes
	cycleDuration := cfg.Layers[numLayers-1].NotifyAfterMinutes
	thresholdMinutes := (cycleDuration+cfg.RepeatIntervalMinutes)*nextRepeat + nextLayer.NotifyAfterMinutes
	elapsed := time.Since(incident.FiredAt)
	if elapsed < time.Duration(thresholdMinutes)*time.Minute {
		return // not yet time for this step
	}

	// Resolve the target user — either a fixed person (user_id) or the current
	// on-call member from a schedule. user_id takes priority when both are set.
	var user *store.TeamMember
	if nextLayer.UserID != "" {
		uid, err := uuid.Parse(nextLayer.UserID)
		if err == nil {
			user, _ = w.db.GetMemberByID(ctx, uid)
		}
	} else {
		user = w.resolveLayerMember(ctx, incident.TeamID, nextLayer.ScheduleID)
	}
	if user == nil {
		// Layer has no on-call coverage right now — advance past it so we
		// don't block subsequent layers.
		advanced, _ := w.db.IncrementEscalationStep(ctx, incident.TeamID, incident.ID, incident.EscalationStep)
		if advanced {
			log.Printf("escalation: incident %s step %d (%s) no coverage, skipping layer", incident.ID, nextStep, nextLayer.LayerName)
		}
		return
	}

	// Atomically claim the escalation step — prevents double-escalation with concurrent workers.
	advanced, err := w.db.IncrementEscalationStep(ctx, incident.TeamID, incident.ID, incident.EscalationStep)
	if err != nil {
		log.Printf("escalation: step increment failed for incident %s: %v", incident.ID, err)
		return
	}
	if !advanced {
		return // another worker already handled this step
	}

	repeatLabel := ""
	if nextRepeat > 0 {
		repeatLabel = fmt.Sprintf(" (repeat %d)", nextRepeat)
	}
	log.Printf("⬆ Escalating incident %s to step %d%s (%s): %s", incident.ID, nextStep, repeatLabel, nextLayer.LayerName, user.Name)
	w.sendNotifications(ctx, incident, user, nil)
}

// resolveLayerMember returns the on-call member for a specific schedule at the
// current time, respecting overrides. Returns nil if no coverage or on error.
func (w *Worker) resolveLayerMember(ctx context.Context, teamID uuid.UUID, scheduleIDStr string) *store.TeamMember {
	sid, err := uuid.Parse(scheduleIDStr)
	if err != nil {
		return nil
	}
	schedule, err := w.db.GetScheduleByID(ctx, sid, teamID)
	if err != nil || schedule == nil {
		return nil
	}
	now := time.Now()

	// Check for active override first.
	override, err := w.db.GetActiveOverrideForSchedule(ctx, schedule.ID, teamID, now)
	if err == nil && override != nil {
		if member, err := w.db.GetMemberByID(ctx, override.UserID); err == nil {
			return member
		}
	}

	// Resolve from rotation.
	results, err := oncall.ResolveAllLayersAt(schedule.RotationConfig, now)
	if err != nil {
		return nil
	}
	for _, r := range results {
		if r.UserID != uuid.Nil {
			if member, err := w.db.GetMemberByID(ctx, r.UserID); err == nil {
				return member
			}
		}
	}
	return nil
}
