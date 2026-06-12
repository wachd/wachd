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
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wachd/wachd/internal/graph"
	"github.com/wachd/wachd/internal/store"
)

func requireServerDB(t *testing.T) *store.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wachd:wachd_dev_password@localhost:5432/wachd?sslmode=disable"
	}
	db, err := store.NewDB(dsn)
	if err != nil {
		t.Skipf("skip: cannot connect to test DB (%v)", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestHandleGetSimilarIncidents_NoGraphNodesReturnsEmptyArray(t *testing.T) {
	db := requireServerDB(t)
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, "graph-unit-"+uuid.NewString()[:8], "secret-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })
	incident := &store.Incident{TeamID: team.ID, Title: "Payment timeout", Severity: "critical", Status: "resolved", Source: "grafana", FiredAt: time.Now().UTC(), AlertPayload: []byte("{}")}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	t.Setenv("AUTH_DISABLED", "true")
	server := &Server{cfg: db, db: db, graphStore: graph.NewPostgresStore(db.Pool())}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = mux.SetURLVars(req, map[string]string{"teamId": team.ID.String(), "incidentId": incident.ID.String()})
	w := httptest.NewRecorder()
	server.handleGetSimilarIncidents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data  []map[string]interface{} `json:"data"`
		Error interface{}              `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty data array, got %+v", resp.Data)
	}
}

func TestHandleGetSimilarIncidents_InvalidTeamID(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = mux.SetURLVars(req, map[string]string{"teamId": "bad", "incidentId": uuid.NewString()})
	w := httptest.NewRecorder()
	server.handleGetSimilarIncidents(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeleteGraphNode_InvalidNodeID(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = mux.SetURLVars(req, map[string]string{"teamId": uuid.NewString(), "nodeId": "bad"})
	w := httptest.NewRecorder()
	server.handleDeleteGraphNode(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpsertGraphConfig_InvalidScore(t *testing.T) {
	t.Setenv("AUTH_DISABLED", "true")
	server := &Server{}
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"enabled":true,"min_similarity_score":1.5}`))
	req = mux.SetURLVars(req, map[string]string{"teamId": uuid.NewString()})
	w := httptest.NewRecorder()
	server.handleUpsertGraphConfig(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}
