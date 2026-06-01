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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/store"
)

func TestLoadSimilarIncidentForNotificationIncludesMatchAboveThreshold(t *testing.T) {
	ctx := context.Background()
	teamID := uuid.New()
	pastIncidentID := uuid.New()
	pastExternalID := pastIncidentID.String()
	pastFiredAt := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC)

	lookup := &similarNotificationLookup{
		graphConfig: &store.TeamGraphConfig{
			TeamID:  teamID,
			Enabled: true,
		},
		incident: &store.Incident{
			ID:      pastIncidentID,
			TeamID:  teamID,
			Title:   "Payment timeout",
			FiredAt: pastFiredAt,
		},
	}

	graphStore := &similarNotificationGraphStore{
		matches: []*graph.SimilarNode{
			{
				Node: &graph.Node{
					ID:         uuid.New(),
					TeamID:     teamID,
					Type:       graph.NodeTypeIncident,
					Status:     graph.NodeStatusPermanent,
					Label:      "Payment timeout",
					ExternalID: &pastExternalID,
					Properties: rawGraphProperties(t, map[string]any{
						"resolution": "rolled back v2.3.1",
					}),
				},
				Score:  0.84,
				Reason: "similar root cause text",
			},
		},
	}

	similar, err := loadSimilarIncidentForNotification(ctx, lookup, graphStore, teamID, func() (*graph.Node, error) {
		return &graph.Node{
			ID:     uuid.New(),
			TeamID: teamID,
			Type:   graph.NodeTypeIncident,
			Label:  "New payment timeout",
		}, nil
	}, "https://wachd.example.com")
	if err != nil {
		t.Fatalf("load similar incident: %v", err)
	}

	if similar == nil {
		t.Fatal("expected similar incident")
	}

	if similar.Title != "Payment timeout" {
		t.Fatalf("expected title from graph node, got %q", similar.Title)
	}

	if similar.Score != 0.84 {
		t.Fatalf("expected score 0.84, got %.2f", similar.Score)
	}

	if similar.Resolution != "rolled back v2.3.1" {
		t.Fatalf("expected previous resolution, got %q", similar.Resolution)
	}

	if !strings.Contains(similar.URL, pastIncidentID.String()) {
		t.Fatalf("expected dashboard URL to contain incident ID, got %q", similar.URL)
	}

	if !similar.FiredAt.Equal(pastFiredAt) {
		t.Fatalf("expected fired_at %s, got %s", pastFiredAt, similar.FiredAt)
	}
}

func TestLoadSimilarIncidentForNotificationOmitsMatchBelowThreshold(t *testing.T) {
	ctx := context.Background()
	teamID := uuid.New()

	graphStore := &similarNotificationGraphStore{
		matches: []*graph.SimilarNode{
			{
				Node: &graph.Node{
					ID:     uuid.New(),
					TeamID: teamID,
					Type:   graph.NodeTypeIncident,
					Label:  "Weak match",
				},
				Score: 0.49,
			},
		},
	}

	similar, err := loadSimilarIncidentForNotification(ctx, enabledGraphLookup(teamID), graphStore, teamID, func() (*graph.Node, error) {
		return &graph.Node{
			ID:     uuid.New(),
			TeamID: teamID,
			Type:   graph.NodeTypeIncident,
			Label:  "New incident",
		}, nil
	}, "https://wachd.example.com")
	if err != nil {
		t.Fatalf("load similar incident: %v", err)
	}

	if similar != nil {
		t.Fatalf("expected no similar incident below threshold, got %+v", similar)
	}
}

func TestLoadSimilarIncidentForNotificationReturnsErrorWhenFindSimilarErrors(t *testing.T) {
	ctx := context.Background()
	teamID := uuid.New()

	graphStore := &similarNotificationGraphStore{
		findErr: errors.New("similarity backend unavailable"),
	}

	similar, err := loadSimilarIncidentForNotification(ctx, enabledGraphLookup(teamID), graphStore, teamID, func() (*graph.Node, error) {
		return &graph.Node{
			ID:     uuid.New(),
			TeamID: teamID,
			Type:   graph.NodeTypeIncident,
			Label:  "New incident",
		}, nil
	}, "https://wachd.example.com")

	if similar != nil {
		t.Fatalf("expected no similar incident when FindSimilar errors, got %+v", similar)
	}

	if err == nil {
		t.Fatal("expected FindSimilar error to be returned to the worker for logging")
	}
}

func TestLoadSimilarIncidentForNotificationTreatsMissingGraphConfigAsEnabled(t *testing.T) {
	ctx := context.Background()
	teamID := uuid.New()

	lookup := &similarNotificationLookup{
		graphConfig: nil,
	}
	graphStore := &similarNotificationGraphStore{}

	buildCalled := false
	similar, err := loadSimilarIncidentForNotification(ctx, lookup, graphStore, teamID, func() (*graph.Node, error) {
		buildCalled = true
		return &graph.Node{
			ID:     uuid.New(),
			TeamID: teamID,
			Type:   graph.NodeTypeIncident,
			Label:  "New incident",
		}, nil
	}, "https://wachd.example.com")
	if err != nil {
		t.Fatalf("load similar incident: %v", err)
	}

	if similar != nil {
		t.Fatalf("expected no similar incident when graph has no matches, got %+v", similar)
	}

	if !buildCalled {
		t.Fatal("expected active incident graph node to be built when config row is missing")
	}

	if !graphStore.upsertCalled {
		t.Fatal("expected graph node to be written when config row is missing")
	}

	if !graphStore.findCalled {
		t.Fatal("expected FindSimilar to run when config row is missing")
	}
}

func TestLoadSimilarIncidentForNotificationOmitsWhenGraphDisabled(t *testing.T) {
	ctx := context.Background()
	teamID := uuid.New()

	lookup := &similarNotificationLookup{
		graphConfig: &store.TeamGraphConfig{
			TeamID:  teamID,
			Enabled: false,
		},
	}
	graphStore := &similarNotificationGraphStore{}

	similar, err := loadSimilarIncidentForNotification(ctx, lookup, graphStore, teamID, func() (*graph.Node, error) {
		t.Fatal("buildNode should not be called when graph is disabled")
		return nil, nil
	}, "https://wachd.example.com")
	if err != nil {
		t.Fatalf("load similar incident: %v", err)
	}

	if similar != nil {
		t.Fatalf("expected no similar incident when graph is disabled, got %+v", similar)
	}

	if graphStore.upsertCalled {
		t.Fatal("expected graph node not to be written when graph is disabled")
	}
}

func enabledGraphLookup(teamID uuid.UUID) *similarNotificationLookup {
	return &similarNotificationLookup{
		graphConfig: &store.TeamGraphConfig{
			TeamID:  teamID,
			Enabled: true,
		},
	}
}

type similarNotificationLookup struct {
	graphConfig *store.TeamGraphConfig
	configErr   error
	incident    *store.Incident
	incidentErr error
}

func (s *similarNotificationLookup) GetTeamGraphConfig(ctx context.Context, teamID uuid.UUID) (*store.TeamGraphConfig, error) {
	return s.graphConfig, s.configErr
}

func (s *similarNotificationLookup) GetIncident(ctx context.Context, teamID uuid.UUID, id uuid.UUID) (*store.Incident, error) {
	if s.incidentErr != nil {
		return nil, s.incidentErr
	}

	if s.incident != nil && s.incident.ID == id && s.incident.TeamID == teamID {
		return s.incident, nil
	}

	return nil, nil
}

type similarNotificationGraphStore struct {
	upsertCalled bool
	findCalled   bool
	upsertErr    error
	findErr      error
	matches      []*graph.SimilarNode
}

func (s *similarNotificationGraphStore) UpsertNode(ctx context.Context, teamID uuid.UUID, n *graph.Node) (*graph.Node, error) {
	s.upsertCalled = true

	if s.upsertErr != nil {
		return nil, s.upsertErr
	}

	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	n.TeamID = teamID

	return n, nil
}

func (s *similarNotificationGraphStore) UpsertEdge(ctx context.Context, teamID uuid.UUID, e *graph.Edge) (*graph.Edge, error) {
	panic("not used")
}

func (s *similarNotificationGraphStore) GetSubgraph(ctx context.Context, teamID uuid.UUID, rootNodeID uuid.UUID, depth int) (*graph.Graph, error) {
	panic("not used")
}

func (s *similarNotificationGraphStore) FindSimilar(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID, limit int) ([]*graph.SimilarNode, error) {
	s.findCalled = true

	if s.findErr != nil {
		return nil, s.findErr
	}

	return s.matches, nil
}

func (s *similarNotificationGraphStore) FindNodeByExternalID(ctx context.Context, teamID uuid.UUID, nodeType graph.NodeType, externalID string) (*graph.Node, error) {
	panic("not used")
}

func (s *similarNotificationGraphStore) ListNodes(ctx context.Context, teamID uuid.UUID, status graph.NodeStatus, limit int) ([]*graph.Node, error) {
	panic("not used")
}

func (s *similarNotificationGraphStore) PromoteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	panic("not used")
}

func (s *similarNotificationGraphStore) DeleteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	panic("not used")
}

func rawGraphProperties(t *testing.T, value any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal graph properties: %v", err)
	}

	return raw
}
