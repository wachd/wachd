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
	"strings"
	"testing"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/sanitiser"
	"github.com/wachd/wachd/internal/store"
)

func TestWorkerSecondIncidentFindsResolvedGraphHistoryForNotification(t *testing.T) {
	db := requireWorkerDB(t)

	ctx := context.Background()

	team, err := db.CreateTeam(ctx, uniqueGraphName("similar-notification"), "secret")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	worker := &Worker{
		db:         db,
		graphStore: graph.NewPostgresStore(db.Pool()),
		sanitiser:  sanitiser.NewSanitiser(),
	}

	previous := createResolvedIncidentFixture(t, ctx, db, team.ID)
	job := &queue.Job{
		Type:       "incident_resolved",
		IncidentID: previous.ID,
		TeamID:     team.ID,
	}
	if err := worker.processResolvedIncidentJob(ctx, job); err != nil {
		t.Fatalf("process resolved incident job: %v", err)
	}

	message := "Checkout is returning 500s after database pool exhaustion"
	current := &store.Incident{
		TeamID:   team.ID,
		Title:    "Checkout database connection pool exhausted",
		Message:  &message,
		Severity: "critical",
		Status:   "open",
		FiredAt:  time.Now().UTC(),
		AlertPayload: []byte(`{
			"labels": {
				"service": "checkout-api"
			},
			"annotations": {
				"summary": "Checkout database pool exhausted"
			}
		}`),
	}
	if err := db.CreateIncident(ctx, current); err != nil {
		t.Fatalf("create current incident: %v", err)
	}

	currentCtx := &correlator.Context{
		Logs: []collector.LogLine{
			{
				Timestamp: time.Now().UTC(),
				Message:   "dial tcp 10.0.0.12:5432 timeout while checkout-api handled request",
				Labels: map[string]string{
					"service": "checkout-api",
				},
			},
		},
		Metrics: []collector.MetricPoint{
			{
				Timestamp: time.Now().UTC(),
				Value:     0.82,
				Labels: map[string]string{
					"__name__": "http_5xx_rate",
					"service":  "checkout-api",
				},
			},
		},
	}
	analysis := &ai.AnalysisResponse{
		RootCause:       "database pool exhaustion",
		SuggestedAction: "increase the checkout database pool size",
		Confidence:      "high",
	}

	similar := worker.findSimilarIncidentForNotification(ctx, current, currentCtx, analysis)
	if similar == nil {
		t.Fatal("expected similar incident notification context")
	}

	if !strings.Contains(similar.Title, "Checkout timeout") {
		t.Fatalf("expected similar title to reference previous incident, got %q", similar.Title)
	}

	for _, raw := range []string{"admin@example.com", "10.0.0.8"} {
		if strings.Contains(similar.Title, raw) {
			t.Fatalf("similar incident title leaked raw PII %q: %q", raw, similar.Title)
		}
	}

	if similar.Score < similarIncidentNotificationThreshold {
		t.Fatalf("expected score >= %.2f, got %.3f", similarIncidentNotificationThreshold, similar.Score)
	}

	if !strings.Contains(similar.Resolution, "rolled back") {
		t.Fatalf("expected previous resolution from graph properties, got %q", similar.Resolution)
	}

	if !strings.Contains(similar.URL, previous.ID.String()) {
		t.Fatalf("expected dashboard URL to include previous incident ID, got %q", similar.URL)
	}
}

func TestWorkerSimilarIncidentNotificationNoGraphHistory(t *testing.T) {
	db := requireWorkerDB(t)

	ctx := context.Background()

	team, err := db.CreateTeam(ctx, uniqueGraphName("similar-notification-empty"), "secret")
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), "DELETE FROM teams WHERE id = $1", team.ID)
	}()

	worker := &Worker{
		db:         db,
		graphStore: graph.NewPostgresStore(db.Pool()),
		sanitiser:  sanitiser.NewSanitiser(),
	}

	message := "Checkout has a new alert with no history"
	current := &store.Incident{
		TeamID:       team.ID,
		Title:        "Checkout first incident",
		Message:      &message,
		Severity:     "warning",
		Status:       "open",
		FiredAt:      time.Now().UTC(),
		AlertPayload: []byte(`{"labels":{"service":"checkout-api"}}`),
	}
	if err := db.CreateIncident(ctx, current); err != nil {
		t.Fatalf("create current incident: %v", err)
	}

	similar := worker.findSimilarIncidentForNotification(ctx, current, &correlator.Context{}, &ai.AnalysisResponse{
		RootCause:       "new issue",
		SuggestedAction: "investigate",
		Confidence:      "medium",
	})
	if similar != nil {
		t.Fatalf("expected no similar incident for empty graph history, got %+v", similar)
	}
}
