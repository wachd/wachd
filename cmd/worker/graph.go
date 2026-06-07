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

const (
	maxGraphLogLines = 3
	maxGraphCommits  = 2

	// edgeSimilarLimit caps how many similar_to edges we write per incident.
	// FindSimilar returns at most this many candidates.
	edgeSimilarLimit = 10

	// edgeSimilarThreshold mirrors the default minimum similarity score in the
	// postgres store. Only matches at or above this score get an edge.
	edgeSimilarThreshold = 0.12
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

	return persistResolvedIncidentNode(ctx, w.db, w.graphStore, incident.TeamID,
		func() (*graph.Node, error) {
			return w.buildResolvedIncidentNode(incident, sanitizedCtx, analysis)
		},
		func(ctx context.Context, nodeID uuid.UUID) error {
			return writeIncidentEdges(ctx, w.graphStore, incident.TeamID, nodeID, sanitizedCtx, analysis)
		},
	)
}

// writePendingIncidentToGraph writes a minimal pending node for the incident
// the moment an alert fires. This lets FindSimilar query against historical
// permanent nodes during the active incident — before resolution data is available.
// Errors are logged and swallowed: graph writes must never block alert routing.
func (w *Worker) writePendingIncidentToGraph(ctx context.Context, incident *store.Incident) {
	if w.graphStore == nil || w.db == nil || incident == nil {
		return
	}

	cfg, err := w.db.GetTeamGraphConfig(ctx, incident.TeamID)
	if err != nil {
		log.Printf("warn: load team graph config for pending node, team %s: %v", incident.TeamID, err)
		return
	}
	if cfg != nil && !cfg.Enabled {
		return
	}

	externalID := incident.ID.String()
	node := &graph.Node{
		Type:       graph.NodeTypeIncident,
		Status:     graph.NodeStatusPending,
		Label:      w.sanitiseGraphValue("Untitled incident", incident.Title),
		ExternalID: &externalID,
	}
	if _, err := w.graphStore.UpsertNode(ctx, incident.TeamID, node); err != nil {
		log.Printf("warn: write pending graph node for incident %s: %v", incident.ID, err)
	}
}

// persistResolvedIncidentNode upserts a pending node for the resolved incident
// and promotes it to permanent. After promotion, writeEdges is called to wire
// similar_to and caused_by relationships. All graph errors are fail-open.
//
// writeEdges may be nil — pass nil to skip edge writing (used in tests).
func persistResolvedIncidentNode(ctx context.Context, cfgStore teamGraphConfigReader, graphStore graph.Store, teamID uuid.UUID, buildNode func() (*graph.Node, error), writeEdges func(ctx context.Context, nodeID uuid.UUID) error) error {
	if graphStore == nil || cfgStore == nil || teamID == uuid.Nil || buildNode == nil {
		return nil
	}

	cfg, err := cfgStore.GetTeamGraphConfig(ctx, teamID)
	if err != nil {
		log.Printf("warn: load team graph config for %s: %v", teamID, err)
		return nil
	}
	// min_similarity_score is intentionally unused on the write path; it applies
	// when FindSimilar reads back permanent nodes.
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

	if writeEdges != nil {
		if err := writeEdges(ctx, created.ID); err != nil {
			log.Printf("warn: write incident graph edges for team %s: %v", teamID, err)
		}
	}

	log.Printf("✓ Resolved incident graph node promoted for team %s", teamID)
	return nil
}

// writeIncidentEdges writes similar_to edges for every past incident whose
// similarity score meets the threshold, and a caused_by edge to the most recent
// deployment commit node when the AI identified a deployment as the root cause.
//
// Both write paths are individually fail-open: an error on one does not abort
// the other. The function always returns nil so the caller can log-and-continue.
func writeIncidentEdges(ctx context.Context, graphStore graph.Store, teamID uuid.UUID, nodeID uuid.UUID, sanitizedCtx *correlator.Context, analysis *ai.AnalysisResponse) error {
	writeSimilarEdges(ctx, graphStore, teamID, nodeID)
	writeCausedByEdge(ctx, graphStore, teamID, nodeID, sanitizedCtx, analysis)
	return nil
}

func writeSimilarEdges(ctx context.Context, graphStore graph.Store, teamID uuid.UUID, nodeID uuid.UUID) {
	similar, err := graphStore.FindSimilar(ctx, teamID, nodeID, edgeSimilarLimit)
	if err != nil {
		log.Printf("warn: find similar for edge writes, team %s: %v", teamID, err)
		return
	}

	for _, match := range similar {
		if match == nil || match.Node == nil || match.Score < edgeSimilarThreshold {
			continue
		}
		edge := &graph.Edge{
			FromNodeID: nodeID,
			ToNodeID:   match.Node.ID,
			Type:       graph.EdgeTypeSimilarTo,
			Status:     graph.EdgeStatusPermanent, // both nodes are permanent at this point
			Weight:     match.Score,
		}
		if _, err := graphStore.UpsertEdge(ctx, teamID, edge); err != nil {
			log.Printf("warn: upsert similar_to edge for team %s node %s: %v", teamID, match.Node.ID, err)
		}
	}
}

func writeCausedByEdge(ctx context.Context, graphStore graph.Store, teamID uuid.UUID, nodeID uuid.UUID, sanitizedCtx *correlator.Context, analysis *ai.AnalysisResponse) {
	if analysis == nil || !analysis.IsDeploymentCause {
		return
	}
	if sanitizedCtx == nil || len(sanitizedCtx.Commits) == 0 {
		return
	}

	// Use the most recent commit (index 0) as the deployment that caused the incident.
	commit := sanitizedCtx.Commits[0]
	sha := strings.TrimSpace(commit.SHA)
	if sha == "" {
		return
	}

	label := strings.TrimSpace(commit.Message)
	if label == "" {
		label = sha
	}

	// Deployment nodes are permanent immediately — they represent immutable history.
	deployNode := &graph.Node{
		Type:       graph.NodeTypeDeployment,
		Status:     graph.NodeStatusPermanent,
		Label:      label,
		ExternalID: &sha,
	}
	created, err := graphStore.UpsertNode(ctx, teamID, deployNode)
	if err != nil {
		log.Printf("warn: upsert deployment node for team %s sha %s: %v", teamID, sha, err)
		return
	}

	edge := &graph.Edge{
		FromNodeID: nodeID,
		ToNodeID:   created.ID,
		Type:       graph.EdgeTypeCausedBy,
		Status:     graph.EdgeStatusPermanent,
		Weight:     1,
	}
	if _, err := graphStore.UpsertEdge(ctx, teamID, edge); err != nil {
		log.Printf("warn: upsert caused_by edge for team %s: %v", teamID, err)
	}
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
	parts := make([]string, 0, maxGraphLogLines)
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
		if len(parts) == maxGraphLogLines {
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
	parts := make([]string, 0, maxGraphCommits)
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
		if len(parts) == maxGraphCommits {
			break
		}
	}
	return strings.Join(parts, " | ")
}
