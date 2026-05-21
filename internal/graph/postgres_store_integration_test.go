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

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return encoded
}
