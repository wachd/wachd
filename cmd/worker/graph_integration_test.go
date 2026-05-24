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
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/sanitiser"
	"github.com/wachd/wachd/internal/store"
)

func requireWorkerDB(t *testing.T) *store.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wachd:wachd_dev_password@localhost:5432/wachd"
	}
	db, err := store.NewDB(dsn)
	if err != nil {
		t.Skipf("skipping integration test: DB unavailable (%v)", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestWorker_ProcessResolvedIncidentJob_WritesPermanentNode(t *testing.T) {
	db := requireWorkerDB(t)
	ctx := context.Background()

	teamA, err := db.CreateTeam(ctx, uniqueGraphName("graph-team-a"), uniqueGraphName("secret-a"))
	if err != nil {
		t.Fatalf("CreateTeam A: %v", err)
	}
	teamB, err := db.CreateTeam(ctx, uniqueGraphName("graph-team-b"), uniqueGraphName("secret-b"))
	if err != nil {
		t.Fatalf("CreateTeam B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id IN ($1, $2)", teamA.ID, teamB.ID)
	})

	incident := createResolvedIncidentFixture(t, ctx, db, teamA.ID)

	worker := &Worker{
		db:         db,
		sanitiser:  sanitiser.NewSanitiser(),
		graphStore: graph.NewPostgresStore(db.Pool()),
	}

	job := &queue.Job{Type: "incident_resolved", IncidentID: incident.ID, TeamID: teamA.ID}
	if err := worker.processResolvedIncidentJob(ctx, job); err != nil {
		t.Fatalf("processResolvedIncidentJob first run: %v", err)
	}
	if err := worker.processResolvedIncidentJob(ctx, job); err != nil {
		t.Fatalf("processResolvedIncidentJob second run: %v", err)
	}

	var nodeID uuid.UUID
	var status string
	err = db.Pool().QueryRow(ctx, `
		SELECT id, status
		FROM graph_nodes
		WHERE team_id = $1 AND type = 'incident' AND external_id = $2
	`, teamA.ID, incident.ID.String()).Scan(&nodeID, &status)
	if err != nil {
		t.Fatalf("query graph node: %v", err)
	}
	if status != "permanent" {
		t.Fatalf("expected permanent node, got %q", status)
	}

	var count int
	if err := db.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_nodes
		WHERE team_id = $1 AND type = 'incident' AND external_id = $2
	`, teamA.ID, incident.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count graph nodes: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one upserted node, got %d", count)
	}

	if err := db.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_nodes
		WHERE team_id = $1 AND external_id = $2
	`, teamB.ID, incident.ID.String()).Scan(&count); err != nil {
		t.Fatalf("cross-team count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no node for other team, got %d", count)
	}
}

func createResolvedIncidentFixture(t *testing.T, ctx context.Context, db *store.DB, teamID uuid.UUID) *store.Incident {
	t.Helper()
	message := "payments failing for admin@example.com"
	incident := &store.Incident{
		TeamID:       teamID,
		Title:        "Checkout timeout for admin@example.com from 10.0.0.8",
		Message:      &message,
		Severity:     "critical",
		Status:       "resolved",
		Source:       "grafana",
		AlertPayload: []byte(`{"tags":{"service":"checkout-api"}}`),
		FiredAt:      time.Now().UTC().Add(-10 * time.Minute),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	sanitizedCtx := &correlator.Context{
		Commits: []collector.Commit{{SHA: "abcdef1234567", Message: "rollback checkout deployment", Author: "[EMAIL]"}},
		Logs:    []collector.LogLine{{Timestamp: time.Now().UTC(), Message: "dial tcp [IP]:5432 timeout", Labels: map[string]string{"service": "checkout-api"}}},
		Metrics: []collector.MetricPoint{{Timestamp: time.Now().UTC(), Value: 95.4}},
	}
	analysis := &ai.AnalysisResponse{
		RootCause:       "database pool exhaustion",
		SuggestedAction: "rolled back checkout deployment",
		Confidence:      "high",
	}
	ctxJSON, _ := json.Marshal(sanitizedCtx)
	analysisJSON, _ := json.Marshal(analysis)
	resolvedAt := time.Now().UTC()
	if _, err := db.Pool().Exec(ctx, `
		UPDATE incidents
		SET status = 'resolved', context = $1, analysis = $2, resolved_at = $3, updated_at = $3
		WHERE id = $4 AND team_id = $5
	`, ctxJSON, analysisJSON, resolvedAt, incident.ID, incident.TeamID); err != nil {
		t.Fatalf("seed resolved incident fields: %v", err)
	}

	loaded, err := db.GetIncident(ctx, incident.TeamID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	return loaded
}

func uniqueGraphName(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8]
}
