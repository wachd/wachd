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

// TestWorker_ProcessResolvedIncidentJob_WritesSimilarToEdges verifies that
// graph_edges gets a similar_to row after two incidents with the same title
// resolve in sequence.
func TestWorker_ProcessResolvedIncidentJob_WritesSimilarToEdges(t *testing.T) {
	db := requireWorkerDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, uniqueGraphName("edge-team"), uniqueGraphName("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	})

	worker := &Worker{
		db:         db,
		sanitiser:  sanitiser.NewSanitiser(),
		graphStore: graph.NewPostgresStore(db.Pool()),
	}

	// Resolve the first incident — no similar nodes exist yet, so no edges.
	first := createResolvedIncidentFixture(t, ctx, db, team.ID)
	job := &queue.Job{Type: "incident_resolved", IncidentID: first.ID, TeamID: team.ID}
	if err := worker.processResolvedIncidentJob(ctx, job); err != nil {
		t.Fatalf("first processResolvedIncidentJob: %v", err)
	}

	// Resolve a second incident with the same title so FindSimilar returns the first.
	second := createResolvedIncidentFixture(t, ctx, db, team.ID)
	job2 := &queue.Job{Type: "incident_resolved", IncidentID: second.ID, TeamID: team.ID}
	if err := worker.processResolvedIncidentJob(ctx, job2); err != nil {
		t.Fatalf("second processResolvedIncidentJob: %v", err)
	}

	var edgeCount int
	if err := db.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes fn ON fn.id = e.from_id AND fn.team_id = $1
		JOIN graph_nodes tn ON tn.id = e.to_id   AND tn.team_id = $1
		WHERE e.team_id = $1 AND e.type = 'similar_to'
	`, team.ID).Scan(&edgeCount); err != nil {
		t.Fatalf("query graph_edges: %v", err)
	}
	if edgeCount == 0 {
		t.Fatal("expected at least one similar_to edge in graph_edges, got 0")
	}
}

// TestWorker_ProcessResolvedIncidentJob_WritesCausedByEdge verifies that a
// caused_by edge and a deployment node are written when IsDeploymentCause=true
// in the stored AI analysis.
func TestWorker_ProcessResolvedIncidentJob_WritesCausedByEdge(t *testing.T) {
	db := requireWorkerDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, uniqueGraphName("causedby-team"), uniqueGraphName("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	})

	worker := &Worker{
		db:         db,
		sanitiser:  sanitiser.NewSanitiser(),
		graphStore: graph.NewPostgresStore(db.Pool()),
	}

	incident := createCausedByIncidentFixture(t, ctx, db, team.ID)
	job := &queue.Job{Type: "incident_resolved", IncidentID: incident.ID, TeamID: team.ID}
	if err := worker.processResolvedIncidentJob(ctx, job); err != nil {
		t.Fatalf("processResolvedIncidentJob: %v", err)
	}

	var edgeCount int
	if err := db.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_edges e
		JOIN graph_nodes fn ON fn.id = e.from_id AND fn.team_id = $1
		JOIN graph_nodes tn ON tn.id = e.to_id   AND tn.team_id = $1
		WHERE e.team_id = $1 AND e.type = 'caused_by'
	`, team.ID).Scan(&edgeCount); err != nil {
		t.Fatalf("query caused_by edges: %v", err)
	}
	if edgeCount == 0 {
		t.Fatal("expected a caused_by edge in graph_edges, got 0")
	}

	// Verify the deployment node was written with the correct external_id.
	var deployCount int
	if err := db.Pool().QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_nodes
		WHERE team_id = $1 AND type = 'deployment' AND external_id = 'deadbeef123'
	`, team.ID).Scan(&deployCount); err != nil {
		t.Fatalf("query deployment node: %v", err)
	}
	if deployCount != 1 {
		t.Fatalf("expected 1 deployment node, got %d", deployCount)
	}
}

// createCausedByIncidentFixture builds a resolved incident whose AI analysis
// sets IsDeploymentCause=true and includes a known commit SHA.
func createCausedByIncidentFixture(t *testing.T, ctx context.Context, db *store.DB, teamID uuid.UUID) *store.Incident {
	t.Helper()
	incident := &store.Incident{
		TeamID:       teamID,
		Title:        "API latency spike caused by deploy",
		Severity:     "high",
		Status:       "resolved",
		Source:       "prometheus",
		AlertPayload: []byte(`{}`),
		FiredAt:      time.Now().UTC().Add(-15 * time.Minute),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	sanitizedCtx := &correlator.Context{
		Commits: []collector.Commit{{SHA: "deadbeef123", Message: "feat: increase thread pool", Author: "[EMAIL]"}},
	}
	analysis := &ai.AnalysisResponse{
		RootCause:         "thread pool exhaustion after deployment",
		SuggestedAction:   "roll back the deployment",
		Confidence:        "high",
		IsDeploymentCause: true,
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

// TestWorker_ProcessAlertJob_WritesPendingNode verifies that a pending graph
// node is written the moment an alert fires, before resolution.
func TestWorker_ProcessAlertJob_WritesPendingNode(t *testing.T) {
	db := requireWorkerDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, uniqueGraphName("pending-team"), uniqueGraphName("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	})

	worker := &Worker{
		db:        db,
		sanitiser: sanitiser.NewSanitiser(),
		graphStore: graph.NewPostgresStore(db.Pool()),
	}

	incident := &store.Incident{
		TeamID:       team.ID,
		Title:        "Database connection refused",
		Severity:     "critical",
		Status:       "firing",
		Source:       "grafana",
		AlertPayload: []byte(`{}`),
		FiredAt:      time.Now().UTC(),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM incidents WHERE id = $1", incident.ID)
	})

	worker.writePendingIncidentToGraph(ctx, incident)

	var status string
	err = db.Pool().QueryRow(ctx, `
		SELECT status FROM graph_nodes
		WHERE team_id = $1 AND type = 'incident' AND external_id = $2
	`, team.ID, incident.ID.String()).Scan(&status)
	if err != nil {
		t.Fatalf("query pending node: %v", err)
	}
	if status != "pending" {
		t.Fatalf("expected pending status, got %q", status)
	}
}
