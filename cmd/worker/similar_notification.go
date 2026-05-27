// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/notify"
	"github.com/wachd/wachd/internal/store"
)

const (
	similarIncidentNotificationThreshold = 0.50
	similarIncidentNotificationLimit     = 1
)

type notificationLookupStore interface {
	GetTeamGraphConfig(ctx context.Context, teamID uuid.UUID) (*store.TeamGraphConfig, error)
	GetIncident(ctx context.Context, teamID uuid.UUID, id uuid.UUID) (*store.Incident, error)
}

func (w *Worker) findSimilarIncidentForNotification(ctx context.Context, incident *store.Incident, sanitizedCtx *correlator.Context, analysis *ai.AnalysisResponse) *notify.SimilarIncident {
	if w == nil || w.db == nil || incident == nil {
		return nil
	}

	similar, err := loadSimilarIncidentForNotification(ctx, w.db, w.graphStore, incident.TeamID, func() (*graph.Node, error) {
		return w.buildResolvedIncidentNode(incident, sanitizedCtx, analysis)
	}, dashboardBaseURL())
	if err != nil {
		log.Printf("warn: similar incident lookup skipped for incident %s: %v", incident.ID, err)
		return nil
	}

	return similar
}

func loadSimilarIncidentForNotification(ctx context.Context, lookup notificationLookupStore, graphStore graph.Store, teamID uuid.UUID, buildNode func() (*graph.Node, error), dashboardBaseURL string) (*notify.SimilarIncident, error) {
	if lookup == nil || graphStore == nil || buildNode == nil {
		return nil, nil
	}

	cfg, err := lookup.GetTeamGraphConfig(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("load graph config: %w", err)
	}
	if cfg != nil && !cfg.Enabled {
		return nil, nil
	}

	node, err := buildNode()
	if err != nil {
		return nil, fmt.Errorf("build active incident graph node: %w", err)
	}
	if node == nil {
		return nil, nil
	}

	node.Status = graph.NodeStatusPending

	node, err = graphStore.UpsertNode(ctx, teamID, node)
	if err != nil {
		return nil, fmt.Errorf("upsert active incident graph node: %w", err)
	}

	matches, err := graphStore.FindSimilar(ctx, teamID, node.ID, similarIncidentNotificationLimit)
	if err != nil {
		return nil, fmt.Errorf("find similar incidents: %w", err)
	}
	if len(matches) == 0 || matches[0] == nil || matches[0].Node == nil {
		return nil, nil
	}
	if matches[0].Score < similarIncidentNotificationThreshold {
		return nil, nil
	}

	return buildSimilarIncidentNotification(ctx, lookup, teamID, matches[0], dashboardBaseURL), nil
}

func buildSimilarIncidentNotification(ctx context.Context, lookup notificationLookupStore, teamID uuid.UUID, match *graph.SimilarNode, dashboardBaseURL string) *notify.SimilarIncident {
	if match == nil || match.Node == nil {
		return nil
	}

	node := match.Node

	similar := &notify.SimilarIncident{
		Title:      strings.TrimSpace(node.Label),
		Score:      match.Score,
		Resolution: extractGraphNodeResolution(node),
	}

	externalID := ""
	if node.ExternalID != nil {
		externalID = strings.TrimSpace(*node.ExternalID)
	}

	if externalID != "" {
		similar.URL = dashboardIncidentURL(dashboardBaseURL, externalID)

		if incidentID, err := uuid.Parse(externalID); err == nil && lookup != nil {
			if incident, err := lookup.GetIncident(ctx, teamID, incidentID); err == nil && incident != nil {
				similar.FiredAt = incident.FiredAt
			}
		}
	}

	if strings.TrimSpace(similar.Title) == "" {
		similar.Title = "Untitled incident"
	}

	return similar
}

func extractGraphNodeResolution(node *graph.Node) string {
	if node == nil || len(node.Properties) == 0 {
		return ""
	}

	var props map[string]any
	if err := json.Unmarshal(node.Properties, &props); err != nil {
		return ""
	}

	for _, key := range []string{"resolution", "resolved_by", "resolvedBy", "fix", "suggested_action", "suggestedAction"} {
		if value, ok := props[key]; ok {
			if text := propertyText(value); text != "" {
				return text
			}
		}
	}

	return ""
}

func propertyText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := propertyText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		for _, key := range []string{"summary", "message", "value", "text"} {
			if text := propertyText(typed[key]); text != "" {
				return text
			}
		}
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func dashboardBaseURL() string {
	for _, key := range []string{"WACHD_DASHBOARD_URL", "WACHD_WEB_URL", "PUBLIC_DASHBOARD_URL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}

	return "http://localhost:3000"
}

func dashboardIncidentURL(baseURL, incidentID string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	incidentID = strings.TrimSpace(incidentID)

	if baseURL == "" || incidentID == "" {
		return ""
	}

	return baseURL + "/incidents/" + url.PathEscape(incidentID)
}
