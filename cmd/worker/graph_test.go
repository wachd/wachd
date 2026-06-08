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

	// edge tracking
	upsertEdgeCalled bool
	upsertEdges      []*graph.Edge
	upsertEdgeErr    error

	// FindSimilar control
	findSimilarNodes []*graph.SimilarNode
	findSimilarErr   error
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
	out := *n
	if out.ID == uuid.Nil {
		out.ID = uuid.New()
	}
	return &out, nil
}

func (m *mockGraphStore) UpsertEdge(_ context.Context, _ uuid.UUID, e *graph.Edge) (*graph.Edge, error) {
	m.upsertEdgeCalled = true
	m.upsertEdges = append(m.upsertEdges, e)
	if m.upsertEdgeErr != nil {
		return nil, m.upsertEdgeErr
	}
	out := *e
	if out.ID == uuid.Nil {
		out.ID = uuid.New()
	}
	return &out, nil
}

func (m *mockGraphStore) GetSubgraph(context.Context, uuid.UUID, uuid.UUID, int) (*graph.Graph, error) {
	panic("unexpected call")
}

func (m *mockGraphStore) FindSimilar(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ int) ([]*graph.SimilarNode, error) {
	if m.findSimilarErr != nil {
		return nil, m.findSimilarErr
	}
	return m.findSimilarNodes, nil
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
		nil,
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
		nil,
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

func TestPersistResolvedIncidentNode_CallsWriteEdgesAfterPromotion(t *testing.T) {
	promotedID := uuid.New()
	graphStore := &mockGraphStore{
		returnedNode: &graph.Node{ID: promotedID},
	}

	var edgeNodeID uuid.UUID
	writeEdgesCalled := false

	err := persistResolvedIncidentNode(
		context.Background(),
		mockGraphConfigStore{cfg: &store.TeamGraphConfig{Enabled: true}},
		graphStore,
		uuid.New(),
		func() (*graph.Node, error) {
			return &graph.Node{Label: "checkout timeout", Type: graph.NodeTypeIncident}, nil
		},
		func(_ context.Context, nodeID uuid.UUID) error {
			writeEdgesCalled = true
			edgeNodeID = nodeID
			return nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !graphStore.promoteCalled {
		t.Fatal("expected PromoteNode to be called")
	}
	if !writeEdgesCalled {
		t.Fatal("expected writeEdges to be called after promotion")
	}
	if edgeNodeID != promotedID {
		t.Fatalf("expected writeEdges to receive promoted node id %s, got %s", promotedID, edgeNodeID)
	}
}

func TestPersistResolvedIncidentNode_WriteEdgesErrorDoesNotBubble(t *testing.T) {
	graphStore := &mockGraphStore{
		returnedNode: &graph.Node{ID: uuid.New()},
	}
	err := persistResolvedIncidentNode(
		context.Background(),
		mockGraphConfigStore{cfg: &store.TeamGraphConfig{Enabled: true}},
		graphStore,
		uuid.New(),
		func() (*graph.Node, error) {
			return &graph.Node{Label: "payment error", Type: graph.NodeTypeIncident}, nil
		},
		func(_ context.Context, _ uuid.UUID) error {
			return errors.New("edge write failure")
		},
	)
	if err != nil {
		t.Fatalf("expected edge write error to be swallowed, got %v", err)
	}
}

func TestWriteIncidentEdges_WritesSimilarToEdges(t *testing.T) {
	teamID := uuid.New()
	nodeID := uuid.New()
	similarNodeID := uuid.New()

	gs := &mockGraphStore{
		findSimilarNodes: []*graph.SimilarNode{
			{Node: &graph.Node{ID: similarNodeID}, Score: 0.75},
		},
	}

	if err := writeIncidentEdges(context.Background(), gs, teamID, nodeID, &correlator.Context{}, nil); err != nil {
		t.Fatalf("writeIncidentEdges: %v", err)
	}
	if !gs.upsertEdgeCalled {
		t.Fatal("expected UpsertEdge to be called for similar_to")
	}
	if len(gs.upsertEdges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(gs.upsertEdges))
	}
	e := gs.upsertEdges[0]
	if e.Type != graph.EdgeTypeSimilarTo {
		t.Fatalf("expected similar_to edge, got %s", e.Type)
	}
	if e.FromNodeID != nodeID || e.ToNodeID != similarNodeID {
		t.Fatalf("edge endpoints wrong: from=%s to=%s", e.FromNodeID, e.ToNodeID)
	}
	if e.Status != graph.EdgeStatusPermanent {
		t.Fatalf("expected permanent edge status, got %s", e.Status)
	}
	if e.Weight != 0.75 {
		t.Fatalf("expected weight=0.75, got %f", e.Weight)
	}
}

func TestWriteIncidentEdges_NoEdgesWhenFindSimilarReturnsEmpty(t *testing.T) {
	// FindSimilar is the authority on the similarity threshold — it filters
	// before returning. The worker trusts the store's result set entirely.
	// Simulate the store returning nothing (all candidates below its threshold).
	gs := &mockGraphStore{
		findSimilarNodes: []*graph.SimilarNode{},
	}
	if err := writeIncidentEdges(context.Background(), gs, uuid.New(), uuid.New(), &correlator.Context{}, nil); err != nil {
		t.Fatalf("writeIncidentEdges: %v", err)
	}
	if gs.upsertEdgeCalled {
		t.Fatal("expected no edges when FindSimilar returns empty")
	}
}

func TestWriteIncidentEdges_WritesCausedByEdge(t *testing.T) {
	teamID := uuid.New()
	nodeID := uuid.New()
	sha := "abc1234"

	gs := &mockGraphStore{} // no similar nodes

	sanitizedCtx := &correlator.Context{
		Commits: []collector.Commit{{SHA: sha, Message: "rollback checkout"}},
	}
	analysis := &ai.AnalysisResponse{IsDeploymentCause: true}

	if err := writeIncidentEdges(context.Background(), gs, teamID, nodeID, sanitizedCtx, analysis); err != nil {
		t.Fatalf("writeIncidentEdges: %v", err)
	}

	// UpsertNode called for the deployment node, then UpsertEdge for caused_by.
	if !gs.upsertCalled {
		t.Fatal("expected UpsertNode for deployment node")
	}
	if !gs.upsertEdgeCalled {
		t.Fatal("expected UpsertEdge for caused_by")
	}
	if len(gs.upsertEdges) != 1 {
		t.Fatalf("expected 1 caused_by edge, got %d", len(gs.upsertEdges))
	}
	e := gs.upsertEdges[0]
	if e.Type != graph.EdgeTypeCausedBy {
		t.Fatalf("expected caused_by edge, got %s", e.Type)
	}
	if e.FromNodeID != nodeID {
		t.Fatalf("expected from=%s, got %s", nodeID, e.FromNodeID)
	}
	if e.Status != graph.EdgeStatusPermanent {
		t.Fatalf("expected permanent status, got %s", e.Status)
	}
}

func TestWriteIncidentEdges_NoCausedByWhenNotDeploymentCause(t *testing.T) {
	gs := &mockGraphStore{}
	sanitizedCtx := &correlator.Context{
		Commits: []collector.Commit{{SHA: "abc1234", Message: "routine deploy"}},
	}
	analysis := &ai.AnalysisResponse{IsDeploymentCause: false}

	if err := writeIncidentEdges(context.Background(), gs, uuid.New(), uuid.New(), sanitizedCtx, analysis); err != nil {
		t.Fatalf("writeIncidentEdges: %v", err)
	}
	// No similar nodes, not a deployment cause → no edges at all.
	if gs.upsertEdgeCalled {
		t.Fatal("expected no edges when IsDeploymentCause is false")
	}
}

func TestWriteIncidentEdges_FindSimilarErrorDoesNotBlockCausedBy(t *testing.T) {
	teamID := uuid.New()
	nodeID := uuid.New()

	gs := &mockGraphStore{
		findSimilarErr: errors.New("db offline"),
	}
	sanitizedCtx := &correlator.Context{
		Commits: []collector.Commit{{SHA: "abc1234", Message: "deploy"}},
	}
	analysis := &ai.AnalysisResponse{IsDeploymentCause: true}

	// FindSimilar fails, but caused_by should still be written.
	if err := writeIncidentEdges(context.Background(), gs, teamID, nodeID, sanitizedCtx, analysis); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gs.upsertEdgeCalled {
		t.Fatal("expected caused_by edge to be written despite FindSimilar failure")
	}
}

func TestWorker_WritePendingIncidentToGraph_SkipsWhenGraphStoreNil(t *testing.T) {
	w := &Worker{db: nil, graphStore: nil, sanitiser: sanitiser.NewSanitiser()}
	// Should not panic.
	w.writePendingIncidentToGraph(context.Background(), &store.Incident{
		ID:     uuid.New(),
		TeamID: uuid.New(),
		Title:  "test",
	})
}
