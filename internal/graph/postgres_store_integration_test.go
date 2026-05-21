//go:build integration

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

package graph

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

func TestPostgresStoreFindSimilar(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}

	ctx := context.Background()

	db, err := store.NewDB(databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	team, err := db.CreateTeam(ctx, "graph-test-"+uuid.NewString(), "secret-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	graphStore := NewPostgresStore(db.Pool())

	sourceExternalID := "source-" + uuid.NewString()
	source, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPending,
		Label:      "Checkout 500 errors after deploy",
		ExternalID: &sourceExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "checkout-api",
			"root_cause":   "database connection pool exhausted",
			"log_patterns": []string{"postgres connection refused"},
		}),
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	similarExternalID := "similar-" + uuid.NewString()
	similar, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPermanent,
		Label:      "Checkout database connection failures",
		ExternalID: &similarExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "checkout-api",
			"root_cause":   "postgres connection pool exhausted",
			"log_patterns": []string{"database connection refused"},
			"resolution":   "increased postgres connection limit",
		}),
	})
	if err != nil {
		t.Fatalf("upsert similar: %v", err)
	}

	unrelatedExternalID := "unrelated-" + uuid.NewString()
	if _, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPermanent,
		Label:      "Email delivery delayed",
		ExternalID: &unrelatedExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "email-worker",
			"root_cause":   "SMTP provider throttling",
			"log_patterns": []string{"smtp 421 rate limit"},
		}),
	}); err != nil {
		t.Fatalf("upsert unrelated: %v", err)
	}

	pendingExternalID := "pending-" + uuid.NewString()
	pending, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPending,
		Label:      "Checkout database connection refused",
		ExternalID: &pendingExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "checkout-api",
			"root_cause":   "postgres connection pool exhausted",
			"log_patterns": []string{"database connection refused"},
		}),
	})
	if err != nil {
		t.Fatalf("upsert pending: %v", err)
	}

	matches, err := graphStore.FindSimilar(ctx, team.ID, source.ID, 2)
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}

	if len(matches) == 0 {
		t.Fatal("expected at least one similar incident")
	}

	if matches[0].Node.ID != similar.ID {
		t.Fatalf("expected best match %s, got %s", similar.ID, matches[0].Node.ID)
	}

	for _, match := range matches {
		if match.Node.ID == pending.ID {
			t.Fatalf("pending node leaked into FindSimilar results: %s", pending.ID)
		}
		if match.Score <= 0 {
			t.Fatalf("expected positive score for match %s", match.Node.ID)
		}
		if match.Reason == "" {
			t.Fatalf("expected reason for match %s", match.Node.ID)
		}
	}
}

func TestPostgresStoreGetSubgraphUsesRecursiveTraversal(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}

	ctx := context.Background()

	db, err := store.NewDB(databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	team, err := db.CreateTeam(ctx, "graph-subgraph-test-"+uuid.NewString(), "secret-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	graphStore := NewPostgresStore(db.Pool())

	rootExternalID := "root-" + uuid.NewString()
	root, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPending,
		Label:      "Active checkout incident",
		ExternalID: &rootExternalID,
		Properties: rawJSON(t, map[string]any{
			"service": "checkout-api",
		}),
	})
	if err != nil {
		t.Fatalf("upsert root: %v", err)
	}

	serviceExternalID := "service-" + uuid.NewString()
	service, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeService,
		Status:     NodeStatusPermanent,
		Label:      "checkout-api",
		ExternalID: &serviceExternalID,
	})
	if err != nil {
		t.Fatalf("upsert service: %v", err)
	}

	deploymentExternalID := "deployment-" + uuid.NewString()
	deployment, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeDeployment,
		Status:     NodeStatusPermanent,
		Label:      "checkout deployment",
		ExternalID: &deploymentExternalID,
	})
	if err != nil {
		t.Fatalf("upsert deployment: %v", err)
	}

	pendingExternalID := "pending-" + uuid.NewString()
	pendingNeighbour, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeService,
		Status:     NodeStatusPending,
		Label:      "draft service",
		ExternalID: &pendingExternalID,
	})
	if err != nil {
		t.Fatalf("upsert pending neighbour: %v", err)
	}

	rootToService, err := graphStore.UpsertEdge(ctx, team.ID, &Edge{
		FromNodeID: root.ID,
		ToNodeID:   service.ID,
		Type:       EdgeTypeAffects,
		Status:     EdgeStatusPermanent,
		Weight:     1,
	})
	if err != nil {
		t.Fatalf("upsert root-to-service edge: %v", err)
	}

	serviceToDeployment, err := graphStore.UpsertEdge(ctx, team.ID, &Edge{
		FromNodeID: service.ID,
		ToNodeID:   deployment.ID,
		Type:       EdgeTypeCausedBy,
		Status:     EdgeStatusPermanent,
		Weight:     1,
	})
	if err != nil {
		t.Fatalf("upsert service-to-deployment edge: %v", err)
	}

	rootToPending, err := graphStore.UpsertEdge(ctx, team.ID, &Edge{
		FromNodeID: root.ID,
		ToNodeID:   pendingNeighbour.ID,
		Type:       EdgeTypeAffects,
		Status:     EdgeStatusPermanent,
		Weight:     1,
	})
	if err != nil {
		t.Fatalf("upsert root-to-pending edge: %v", err)
	}

	depthOne, err := graphStore.GetSubgraph(ctx, team.ID, root.ID, 1)
	if err != nil {
		t.Fatalf("get depth-one subgraph: %v", err)
	}

	requireGraphHasNode(t, depthOne, root.ID)
	requireGraphHasNode(t, depthOne, service.ID)
	requireGraphDoesNotHaveNode(t, depthOne, deployment.ID)
	requireGraphDoesNotHaveNode(t, depthOne, pendingNeighbour.ID)

	requireGraphHasEdge(t, depthOne, rootToService.ID)
	requireGraphDoesNotHaveEdge(t, depthOne, serviceToDeployment.ID)
	requireGraphDoesNotHaveEdge(t, depthOne, rootToPending.ID)

	depthTwo, err := graphStore.GetSubgraph(ctx, team.ID, root.ID, 2)
	if err != nil {
		t.Fatalf("get depth-two subgraph: %v", err)
	}

	requireGraphHasNode(t, depthTwo, root.ID)
	requireGraphHasNode(t, depthTwo, service.ID)
	requireGraphHasNode(t, depthTwo, deployment.ID)
	requireGraphDoesNotHaveNode(t, depthTwo, pendingNeighbour.ID)

	requireGraphHasEdge(t, depthTwo, rootToService.ID)
	requireGraphHasEdge(t, depthTwo, serviceToDeployment.ID)
	requireGraphDoesNotHaveEdge(t, depthTwo, rootToPending.ID)
}

func requireGraphHasNode(t *testing.T, graph *Graph, nodeID uuid.UUID) {
	t.Helper()

	for _, node := range graph.Nodes {
		if node.ID == nodeID {
			return
		}
	}

	t.Fatalf("expected graph to contain node %s", nodeID)
}

func requireGraphDoesNotHaveNode(t *testing.T, graph *Graph, nodeID uuid.UUID) {
	t.Helper()

	for _, node := range graph.Nodes {
		if node.ID == nodeID {
			t.Fatalf("expected graph not to contain node %s", nodeID)
		}
	}
}

func requireGraphHasEdge(t *testing.T, graph *Graph, edgeID uuid.UUID) {
	t.Helper()

	for _, edge := range graph.Edges {
		if edge.ID == edgeID {
			return
		}
	}

	t.Fatalf("expected graph to contain edge %s", edgeID)
}

func requireGraphDoesNotHaveEdge(t *testing.T, graph *Graph, edgeID uuid.UUID) {
	t.Helper()

	for _, edge := range graph.Edges {
		if edge.ID == edgeID {
			t.Fatalf("expected graph not to contain edge %s", edgeID)
		}
	}
}

func TestPostgresStoreFindSimilarIncludesOlderIncidents(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}

	ctx := context.Background()

	db, err := store.NewDB(databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	team, err := db.CreateTeam(ctx, "graph-old-similar-test-"+uuid.NewString(), "secret-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	graphStore := NewPostgresStore(db.Pool())

	oldSimilarExternalID := "old-similar-" + uuid.NewString()
	oldSimilar, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPermanent,
		Label:      "Checkout database connection pool exhausted",
		ExternalID: &oldSimilarExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "checkout-api",
			"root_cause":   "postgres connection pool exhausted",
			"log_patterns": []string{"database connection refused", "timeout waiting for postgres connection"},
			"resolution":   "increased postgres connection pool size",
		}),
	})
	if err != nil {
		t.Fatalf("upsert old similar incident: %v", err)
	}

	_, err = db.Pool().Exec(ctx, `
		UPDATE graph_nodes
		SET updated_at = now() - interval '30 days'
		WHERE id = $1
	`, oldSimilar.ID)
	if err != nil {
		t.Fatalf("make old similar incident older: %v", err)
	}

	for i := 0; i < 205; i++ {
		unrelatedExternalID := "unrelated-" + uuid.NewString()
		if _, err := graphStore.UpsertNode(ctx, team.ID, &Node{
			Type:       NodeTypeIncident,
			Status:     NodeStatusPermanent,
			Label:      "Email delivery delayed",
			ExternalID: &unrelatedExternalID,
			Properties: rawJSON(t, map[string]any{
				"service":      "email-worker",
				"root_cause":   "third party SMTP provider throttling",
				"log_patterns": []string{"smtp 421 rate limited"},
				"resolution":   "queued retries and waited for provider limit reset",
			}),
		}); err != nil {
			t.Fatalf("upsert unrelated incident %d: %v", i, err)
		}
	}

	sourceExternalID := "source-" + uuid.NewString()
	source, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPending,
		Label:      "Checkout 500 errors after deploy",
		ExternalID: &sourceExternalID,
		Properties: rawJSON(t, map[string]any{
			"service":      "checkout-api",
			"root_cause":   "database connection pool exhausted",
			"log_patterns": []string{"postgres connection refused", "timeout acquiring database connection"},
		}),
	})
	if err != nil {
		t.Fatalf("upsert source incident: %v", err)
	}

	matches, err := graphStore.FindSimilar(ctx, team.ID, source.ID, 1)
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected one match, got %d", len(matches))
	}

	if matches[0].Node.ID != oldSimilar.ID {
		t.Fatalf("expected old similar incident %s, got %s", oldSimilar.ID, matches[0].Node.ID)
	}

	if matches[0].Score <= 0 {
		t.Fatalf("expected positive similarity score, got %.3f", matches[0].Score)
	}

	if matches[0].Reason == "" {
		t.Fatal("expected similarity reason")
	}
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return encoded
}
