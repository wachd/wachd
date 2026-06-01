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
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

func TestPostgresStoreListNodesFiltersByStatus(t *testing.T) {
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

	team, err := db.CreateTeam(ctx, "graph-list-nodes-"+uuid.NewString(), "secret-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	graphStore := NewPostgresStore(db.Pool())

	permanentExternalID := "permanent-" + uuid.NewString()
	permanent, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPermanent,
		Label:      "Permanent incident",
		ExternalID: &permanentExternalID,
	})
	if err != nil {
		t.Fatalf("upsert permanent node: %v", err)
	}

	pendingExternalID := "pending-" + uuid.NewString()
	pending, err := graphStore.UpsertNode(ctx, team.ID, &Node{
		Type:       NodeTypeIncident,
		Status:     NodeStatusPending,
		Label:      "Pending incident",
		ExternalID: &pendingExternalID,
	})
	if err != nil {
		t.Fatalf("upsert pending node: %v", err)
	}

	permanentNodes, err := graphStore.ListNodes(ctx, team.ID, NodeStatusPermanent, 10)
	if err != nil {
		t.Fatalf("list permanent nodes: %v", err)
	}

	requireNodeIDPresent(t, permanentNodes, permanent.ID)
	requireNodeIDAbsent(t, permanentNodes, pending.ID)

	pendingNodes, err := graphStore.ListNodes(ctx, team.ID, NodeStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending nodes: %v", err)
	}

	requireNodeIDPresent(t, pendingNodes, pending.ID)
	requireNodeIDAbsent(t, pendingNodes, permanent.ID)
}

func requireNodeIDPresent(t *testing.T, nodes []*Node, id uuid.UUID) {
	t.Helper()

	for _, node := range nodes {
		if node.ID == id {
			return
		}
	}

	t.Fatalf("expected node %s to be present", id)
}

func requireNodeIDAbsent(t *testing.T, nodes []*Node, id uuid.UUID) {
	t.Helper()

	for _, node := range nodes {
		if node.ID == id {
			t.Fatalf("expected node %s to be absent", id)
		}
	}
}
