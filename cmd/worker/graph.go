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
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/store"
)

type teamGraphConfigReader interface {
	GetTeamGraphConfig(ctx context.Context, teamID uuid.UUID) (*store.TeamGraphConfig, error)
}

func (w *Worker) writeResolvedIncidentToGraph(ctx context.Context, incident *store.Incident) error {
	if incident == nil {
		return nil
	}
	sanitizedCtx := w.loadSanitizedIncidentContext(incident)
	analysis := w.loadIncidentAnalysis(incident)

	return persistResolvedIncidentNode(ctx, w.db, w.graphStore, incident.TeamID, func() (*graph.Node, error) {
		return w.buildResolvedIncidentNode(incident, sanitizedCtx, analysis)
	})
}

func persistResolvedIncidentNode(ctx context.Context, cfgStore teamGraphConfigReader, graphStore graph.Store, teamID uuid.UUID, buildNode func() (*graph.Node, error)) error {
	if graphStore == nil || cfgStore == nil || teamID == uuid.Nil || buildNode == nil {
		return nil
	}

	cfg, err := cfgStore.GetTeamGraphConfig(ctx, teamID)
	if err != nil {
		log.Printf("warn: load team graph config for %s: %v", teamID, err)
		return nil
	}
	if cfg != nil && !cfg.Enabled {
		return nil
	}

	node, err := buildNode()
	if err != nil {
		log.Printf("warn: build resolved graph node for team %s: %v", teamID, err)
		return nil
	}
	if node == nil {
		return nil
	}

	created, err := graphStore.UpsertNode(ctx, teamID, node)
	if err != nil {
		log.Printf("warn: upsert graph node for team %s: %v", teamID, err)
		return nil
	}
	if err := graphStore.PromoteNode(ctx, teamID, created.ID); err != nil {
		log.Printf("warn: promote graph node for team %s: %v", teamID, err)
		return nil
	}

	log.Printf("✓ Resolved incident graph node promoted for team %s", teamID)
	return nil
}

func (w *Worker) buildResolvedIncidentNode(incident *store.Incident, sanitizedCtx *correlator.Context, analysis *ai.AnalysisResponse) (*graph.Node, error) {
	if incident == nil {
		return nil, nil
	}
	if sanitizedCtx == nil {
		sanitizedCtx = &correlator.Context{}
	}

	properties := map[string]any{
		"root_cause":     w.sanitiseGraphValue("", analysisField(analysis, func(a *ai.AnalysisResponse) string { return a.RootCause })),
		"log_pattern":    w.sanitiseGraphValue("", summarizeLogPattern(sanitizedCtx.Logs)),
		"service":        w.sanitiseGraphValue("", w.extractServiceName(incident)),
		"metric_anomaly": w.sanitiseGraphValue("", summarizeMetricAnomaly(sanitizedCtx.Metrics)),
		"deployment":     w.sanitiseGraphValue("", summarizeDeployment(sanitizedCtx.Commits)),
		"resolution":     w.sanitiseGraphValue("", analysisField(analysis, func(a *ai.AnalysisResponse) string { return a.SuggestedAction })),
	}

	propsJSON, err := json.Marshal(properties)
	if err != nil {
		return nil, err
	}

	externalID := incident.ID.String()
	return &graph.Node{
		Type:       graph.NodeTypeIncident,
		Status:     graph.NodeStatusPending,
		Label:      w.sanitiseGraphValue("Untitled incident", incident.Title),
		ExternalID: &externalID,
		Properties: propsJSON,
	}, nil
}

func (w *Worker) loadSanitizedIncidentContext(incident *store.Incident) *correlator.Context {
	ctx := &correlator.Context{}
	if incident == nil || len(incident.Context) == 0 {
		return ctx
	}
	if err := json.Unmarshal(incident.Context, ctx); err != nil {
		log.Printf("warn: parse incident context for %s: %v", incident.ID, err)
		return &correlator.Context{}
	}
	return w.sanitizeContext(ctx)
}

func (w *Worker) loadIncidentAnalysis(incident *store.Incident) *ai.AnalysisResponse {
	if incident == nil || len(incident.Analysis) == 0 {
		return nil
	}
	var analysis ai.AnalysisResponse
	if err := json.Unmarshal(incident.Analysis, &analysis); err != nil {
		log.Printf("warn: parse incident analysis for %s: %v", incident.ID, err)
		return nil
	}
	analysis.RootCause = w.sanitiseGraphValue("", analysis.RootCause)
	analysis.SuggestedAction = w.sanitiseGraphValue("", analysis.SuggestedAction)
	for i, finding := range analysis.KeyFindings {
		analysis.KeyFindings[i] = w.sanitiseGraphValue("", finding)
	}
	return &analysis
}

func (w *Worker) sanitiseGraphValue(fallback, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if w.sanitiser != nil {
		value = w.sanitiser.Sanitise(value)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func analysisField(analysis *ai.AnalysisResponse, getter func(*ai.AnalysisResponse) string) string {
	if analysis == nil {
		return ""
	}
	return getter(analysis)
}

func summarizeLogPattern(logs []collector.LogLine) string {
	if len(logs) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(logs))
	parts := make([]string, 0, 3)
	for _, entry := range logs {
		msg := strings.TrimSpace(entry.Message)
		if msg == "" {
			continue
		}
		if _, ok := seen[msg]; ok {
			continue
		}
		seen[msg] = struct{}{}
		parts = append(parts, msg)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " | ")
}

func summarizeMetricAnomaly(metrics []collector.MetricPoint) string {
	if len(metrics) == 0 {
		return ""
	}
	return formatMetricsSummary(metrics)
}

func summarizeDeployment(commits []collector.Commit) string {
	if len(commits) == 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	for _, commit := range commits {
		msg := strings.TrimSpace(commit.Message)
		if msg == "" {
			continue
		}
		sha := strings.TrimSpace(commit.SHA)
		if len(sha) > 7 {
			sha = sha[:7]
		}
		if sha != "" {
			parts = append(parts, sha+": "+msg)
		} else {
			parts = append(parts, msg)
		}
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " | ")
}
