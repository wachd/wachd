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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/sanitiser"
	"github.com/wachd/wachd/internal/store"
)

type mockGraphStore struct {
	upsertCalled  bool
	promoteCalled bool
	upsertNode    *graph.Node
	promoteNodeID uuid.UUID
	upsertErr     error
	promoteErr    error
	returnedNode  *graph.Node
}

func (m *mockGraphStore) UpsertNode(_ context.Context, _ uuid.UUID, n *graph.Node) (*graph.Node, error) {
	m.upsertCalled = true
	m.upsertNode = n
	if m.upsertErr != nil {
		return nil, m.upsertErr
	}
	if m.returnedNode != nil {
		return m.returnedNode, nil
	}
	return &graph.Node{ID: uuid.New()}, nil
}

func (m *mockGraphStore) UpsertEdge(context.Context, uuid.UUID, *graph.Edge) (*graph.Edge, error) {
	panic("unexpected call")
}

func (m *mockGraphStore) GetSubgraph(context.Context, uuid.UUID, uuid.UUID, int) (*graph.Graph, error) {
	panic("unexpected call")
}

func (m *mockGraphStore) FindSimilar(context.Context, uuid.UUID, uuid.UUID, int) ([]*graph.SimilarNode, error) {
	panic("unexpected call")
}

func (m *mockGraphStore) FindNodeByExternalID(context.Context, uuid.UUID, graph.NodeType, string) (*graph.Node, error) {
	panic("unexpected call")
}

func (m *mockGraphStore) ListNodes(ctx context.Context, teamID uuid.UUID, status graph.NodeStatus, limit int) ([]*graph.Node, error) {
	panic("not used")
}

func (m *mockGraphStore) PromoteNode(_ context.Context, _ uuid.UUID, nodeID uuid.UUID) error {
	m.promoteCalled = true
	m.promoteNodeID = nodeID
	return m.promoteErr
}

func (m *mockGraphStore) DeleteNode(context.Context, uuid.UUID, uuid.UUID) error {
	panic("unexpected call")
}

type mockGraphConfigStore struct {
	cfg *store.TeamGraphConfig
	err error
}

func (m mockGraphConfigStore) GetTeamGraphConfig(context.Context, uuid.UUID) (*store.TeamGraphConfig, error) {
	return m.cfg, m.err
}

func TestWorker_BuildResolvedIncidentNode_SanitisesAndMapsProperties(t *testing.T) {
	w := &Worker{sanitiser: sanitiser.NewSanitiser()}
	message := "pager triggered for oncall@example.com"
	incident := &store.Incident{
		ID:           uuid.New(),
		TeamID:       uuid.New(),
		Title:        "Payment timeout for admin@example.com from 10.0.0.4",
		Message:      &message,
		Severity:     "critical",
		Source:       "grafana",
		AlertPayload: []byte(`{"tags":{"service":"checkout-api 10.0.0.4"}}`),
	}
	ctx := &correlator.Context{
		Commits: []collector.Commit{{SHA: "abcdef123456", Message: "rollback by admin@example.com", Author: "ops@example.com"}},
		Logs:    []collector.LogLine{{Timestamp: time.Now(), Message: "dial tcp 10.0.0.4:5432 for admin@example.com", Labels: map[string]string{"email": "admin@example.com"}}},
		Metrics: []collector.MetricPoint{{Timestamp: time.Now(), Value: 91.2}},
	}
	analysis := &ai.AnalysisResponse{
		RootCause:       "DB pool exhausted for admin@example.com",
		SuggestedAction: "Rollback deploy from 10.0.0.4",
	}

	node, err := w.buildResolvedIncidentNode(incident, ctx, analysis)
	if err != nil {
		t.Fatalf("buildResolvedIncidentNode: %v", err)
	}
	if node.Type != graph.NodeTypeIncident {
		t.Fatalf("expected incident node, got %s", node.Type)
	}
	if node.Status != graph.NodeStatusPending {
		t.Fatalf("expected pending node before promotion, got %s", node.Status)
	}
	if strings.Contains(node.Label, "admin@example.com") || strings.Contains(node.Label, "10.0.0.4") {
		t.Fatalf("expected node label to be sanitised, got %q", node.Label)
	}

	var props map[string]string
	if err := json.Unmarshal(node.Properties, &props); err != nil {
		t.Fatalf("unmarshal properties: %v", err)
	}
	for _, key := range []string{"root_cause", "log_pattern", "service", "metric_anomaly", "deployment", "resolution"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing property %q in %+v", key, props)
		}
	}
	joined := string(node.Properties)
	for _, raw := range []string{"admin@example.com", "ops@example.com", "10.0.0.4"} {
		if strings.Contains(joined, raw) {
			t.Fatalf("properties leaked raw PII %q: %s", raw, joined)
		}
	}
}

func TestPersistResolvedIncidentNode_SkipsWhenGraphDisabled(t *testing.T) {
	buildCalled := false
	graphStore := &mockGraphStore{}
	err := persistResolvedIncidentNode(
		context.Background(),
		mockGraphConfigStore{cfg: &store.TeamGraphConfig{Enabled: false}},
		graphStore,
		uuid.New(),
		func() (*graph.Node, error) {
			buildCalled = true
			return &graph.Node{Label: "never-called", Type: graph.NodeTypeIncident}, nil
		},
	)
	if err != nil {
		t.Fatalf("persistResolvedIncidentNode: %v", err)
	}
	if buildCalled {
		t.Fatal("expected graph node builder to be skipped when graph is disabled")
	}
	if graphStore.upsertCalled || graphStore.promoteCalled {
		t.Fatal("expected no graph writes when graph is disabled")
	}
}

func TestPersistResolvedIncidentNode_PromoteErrorDoesNotBubble(t *testing.T) {
	teamID := uuid.New()
	graphStore := &mockGraphStore{
		returnedNode: &graph.Node{ID: uuid.New()},
		promoteErr:   errors.New("boom"),
	}
	err := persistResolvedIncidentNode(
		context.Background(),
		mockGraphConfigStore{cfg: &store.TeamGraphConfig{Enabled: true}},
		graphStore,
		teamID,
		func() (*graph.Node, error) {
			return &graph.Node{Label: "sanitised incident", Type: graph.NodeTypeIncident}, nil
		},
	)
	if err != nil {
		t.Fatalf("expected fail-safe nil error, got %v", err)
	}
	if !graphStore.upsertCalled {
		t.Fatal("expected upsert to be attempted")
	}
	if !graphStore.promoteCalled {
		t.Fatal("expected promote to be attempted")
	}
}
