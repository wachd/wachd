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

//go:build integration

// End-to-end integration tests for the multi-team alert pipeline and tenant isolation.
//
// Tests two teams (Kong + Kafka) as independent tenants and verifies:
//   - Each team's webhook creates incidents visible only to that team
//   - Cross-team API access is rejected (403)
//   - Team config changes are isolated — one team cannot read or modify another's config
//   - Members endpoint respects team boundaries
//   - Invalid or swapped webhook secrets are rejected
//
// Requires a running PostgreSQL and Redis. Defaults to the dev instances.
// Override with TEST_DATABASE_URL and TEST_REDIS_URL environment variables.
//
// Run with:
//
//	make test-e2e
//
// or directly:
//
//	go test -tags integration -v -count=1 -run TestE2E ./cmd/server/
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/license"
	"github.com/wachd/wachd/internal/oncall"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/store"
)

// ── test harness ──────────────────────────────────────────────────────────────

type e2eEnv struct {
	db          *store.DB
	router      *mux.Router
	kongTeam    *store.Team
	kafkaTeam   *store.Team
	kongCookie  *http.Cookie // admin session scoped to Kong team only
	kafkaCookie *http.Cookie // admin session scoped to Kafka team only
}

// newE2EEnv spins up a full Server with real Postgres + Redis, creates two
// isolated test teams, and returns team-scoped cookies for each.
func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://wachd:wachd_dev_password@localhost:5432/wachd?sslmode=disable"
	}
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	db, err := store.NewDB(dbURL)
	if err != nil {
		t.Skipf("skip: cannot connect to test DB (%v) — set TEST_DATABASE_URL", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Redis client for sessions
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rc := redis.NewClient(opt)
	t.Cleanup(func() { _ = rc.Close() })

	sessions := auth.NewSessionStore(rc)

	// Redis-backed queue for alert jobs
	q, err := queue.NewQueue(redisURL)
	if err != nil {
		t.Skipf("skip: cannot connect to Redis queue (%v) — set TEST_REDIS_URL", err)
	}
	t.Cleanup(func() { _ = q.Close() })

	// Fixed test encryption key (not secret — tests only)
	enc, err := auth.NewEncryptor("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	// Use OSS license with elevated alert limit so test runs don't hit the cap
	lic := license.OSS()
	lic.MaxAlertsMonth = 999_999

	oncallMgr := oncall.NewManager(db)

	// Create isolated test teams — unique name per run avoids collisions
	ts := time.Now().UnixNano()
	kongTeam, err := db.GetOrCreateTeamByName(context.Background(),
		fmt.Sprintf("e2e_kong_%d", ts))
	if err != nil {
		t.Fatalf("create kong team: %v", err)
	}
	kafkaTeam, err := db.GetOrCreateTeamByName(context.Background(),
		fmt.Sprintf("e2e_kafka_%d", ts))
	if err != nil {
		t.Fatalf("create kafka team: %v", err)
	}
	t.Cleanup(func() {
		_ = db.DeleteTeam(context.Background(), kongTeam.ID)
		_ = db.DeleteTeam(context.Background(), kafkaTeam.ID)
	})

	server := &Server{
		db:            db,
		queue:         q,
		oncallManager: oncallMgr,
		sessions:      sessions,
		license:       lic,
		enc:           enc,
	}

	// Create per-team admin sessions. Sessions are stored in Redis and carry
	// the Roles map directly — no DB user required for the e2e harness.
	kongCookie := e2eSession(t, sessions, kongTeam.ID, "admin")
	kafkaCookie := e2eSession(t, sessions, kafkaTeam.ID, "admin")

	// Build router — mirrors the relevant portion of main.go
	router := mux.NewRouter()

	// Webhook endpoint — no auth, validated by secret
	router.HandleFunc("/api/v1/webhook/{teamId}/{secret}",
		server.handleWebhook).Methods("POST")

	// Protected team endpoints
	authMW := auth.BearerOrCookie(sessions, db)
	api := router.PathPrefix("/api/v1/teams").Subrouter()
	api.Use(authMW)
	api.Use(auth.RequireNoForceChange)
	api.HandleFunc("/{teamId}/incidents", server.handleListIncidents).Methods("GET")
	api.HandleFunc("/{teamId}/incidents/{incidentId}", server.handleGetIncident).Methods("GET")
	api.HandleFunc("/{teamId}/members", server.handleListMembers).Methods("GET")
	api.HandleFunc("/{teamId}/config", server.handleGetTeamConfig).Methods("GET")
	api.HandleFunc("/{teamId}/config", server.handleUpsertTeamConfig).Methods("PUT")

	return &e2eEnv{
		db:          db,
		router:      router,
		kongTeam:    kongTeam,
		kafkaTeam:   kafkaTeam,
		kongCookie:  kongCookie,
		kafkaCookie: kafkaCookie,
	}
}

// e2eSession creates a Redis-backed session with the given team role.
// No local_users row is needed — the session carries all necessary claims.
func e2eSession(t *testing.T, sessions *auth.SessionStore, teamID uuid.UUID, role string) *http.Cookie {
	t.Helper()
	sess := &auth.Session{
		Email:               fmt.Sprintf("e2e-%s@test.local", teamID.String()[:8]),
		Name:                fmt.Sprintf("E2E User %s", teamID.String()[:8]),
		AuthType:            "local",
		IsSuperAdmin:        false,
		ForcePasswordChange: false,
		TeamIDs:             []uuid.UUID{teamID},
		Roles:               map[string]string{teamID.String(): role},
		ExpiresAt:           time.Now().Add(1 * time.Hour),
	}
	token, err := sessions.Create(context.Background(), sess)
	if err != nil {
		t.Fatalf("create session for team %s: %v", teamID, err)
	}
	return &http.Cookie{Name: "wachd_session", Value: token}
}

func (e *e2eEnv) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

// e2eBody marshals v to JSON and returns a *bytes.Buffer ready for httptest.
func e2eBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

// fireWebhook POSTs a Grafana-style alert to the given team's webhook URL.
// Returns the parsed JSON response body.
func fireWebhook(t *testing.T, e *e2eEnv, team *store.Team, title, service string) map[string]any {
	t.Helper()
	body := e2eBody(t, map[string]any{
		"title":    title,
		"state":    "alerting",
		"ruleName": "e2e_test_rule",
		"message":  "E2E test alert — ignore",
		"tags": map[string]string{
			"service": service,
			"env":     "e2e",
		},
		"evalMatches": []map[string]any{
			{"metric": "error_rate", "value": 0.05},
		},
	})
	req := httptest.NewRequest("POST",
		fmt.Sprintf("/api/v1/webhook/%s/%s", team.ID, team.WebhookSecret),
		body)
	req.Header.Set("Content-Type", "application/json")

	resp := e.do(req)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("fireWebhook for team %q: got %d, want 202\nbody: %s",
			team.Name, resp.Code, resp.Body)
	}

	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result
}

// ── tests ──────────────────────────────────────────────────────────────────────

// TestE2E_TwoTeams_WebhookCreatesIsolatedIncidents fires one alert per team
// and verifies each team sees only its own incident.
func TestE2E_TwoTeams_WebhookCreatesIsolatedIncidents(t *testing.T) {
	env := newE2EEnv(t)

	// Fire one alert for each team
	kongResp := fireWebhook(t, env, env.kongTeam, "Kong: high error rate on kong-proxy", "kong-proxy")
	kafkaResp := fireWebhook(t, env, env.kafkaTeam, "Kafka: consumer lag on kafka-broker", "kafka-broker")

	kongIncidentID, _ := kongResp["incident_id"].(string)
	kafkaIncidentID, _ := kafkaResp["incident_id"].(string)

	if kongIncidentID == "" {
		t.Fatal("kong webhook response missing incident_id")
	}
	if kafkaIncidentID == "" {
		t.Fatal("kafka webhook response missing incident_id")
	}
	if kongIncidentID == kafkaIncidentID {
		t.Errorf("both webhooks produced the same incident ID (%s) — must be unique", kongIncidentID)
	}

	listIncidents := func(t *testing.T, cookie *http.Cookie, teamID uuid.UUID) []map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET",
			fmt.Sprintf("/api/v1/teams/%s/incidents", teamID), nil)
		req.AddCookie(cookie)
		resp := env.do(req)
		if resp.Code != http.StatusOK {
			t.Fatalf("list incidents for team %s: got %d, want 200", teamID, resp.Code)
		}
		var body struct {
			Incidents []map[string]any `json:"incidents"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return body.Incidents
	}

	// ── Kong admin sees Kong incident, not Kafka ──
	kongIncidents := listIncidents(t, env.kongCookie, env.kongTeam.ID)

	var foundKongInc bool
	for _, inc := range kongIncidents {
		if inc["id"] == kongIncidentID {
			foundKongInc = true
			if inc["title"] != "Kong: high error rate on kong-proxy" {
				t.Errorf("kong incident title: got %v, want 'Kong: high error rate on kong-proxy'",
					inc["title"])
			}
		}
		if inc["id"] == kafkaIncidentID {
			t.Errorf("kafka incident %s leaked into kong team's incident list", kafkaIncidentID)
		}
	}
	if !foundKongInc {
		t.Errorf("kong incident %s not found in kong team list", kongIncidentID)
	}

	// ── Kafka admin sees Kafka incident, not Kong ──
	kafkaIncidents := listIncidents(t, env.kafkaCookie, env.kafkaTeam.ID)

	var foundKafkaInc bool
	for _, inc := range kafkaIncidents {
		if inc["id"] == kafkaIncidentID {
			foundKafkaInc = true
			if inc["title"] != "Kafka: consumer lag on kafka-broker" {
				t.Errorf("kafka incident title: got %v, want 'Kafka: consumer lag on kafka-broker'",
					inc["title"])
			}
		}
		if inc["id"] == kongIncidentID {
			t.Errorf("kong incident %s leaked into kafka team's incident list", kongIncidentID)
		}
	}
	if !foundKafkaInc {
		t.Errorf("kafka incident %s not found in kafka team list", kafkaIncidentID)
	}

	// ── Each incident has the correct team_id ──
	getIncident := func(t *testing.T, cookie *http.Cookie, teamID uuid.UUID, incidentID string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET",
			fmt.Sprintf("/api/v1/teams/%s/incidents/%s", teamID, incidentID), nil)
		req.AddCookie(cookie)
		resp := env.do(req)
		if resp.Code != http.StatusOK {
			t.Fatalf("get incident %s: got %d, want 200", incidentID, resp.Code)
		}
		var inc map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&inc)
		return inc
	}

	kongInc := getIncident(t, env.kongCookie, env.kongTeam.ID, kongIncidentID)
	if kongInc["team_id"] != env.kongTeam.ID.String() {
		t.Errorf("kong incident team_id: got %v, want %s", kongInc["team_id"], env.kongTeam.ID)
	}

	kafkaInc := getIncident(t, env.kafkaCookie, env.kafkaTeam.ID, kafkaIncidentID)
	if kafkaInc["team_id"] != env.kafkaTeam.ID.String() {
		t.Errorf("kafka incident team_id: got %v, want %s", kafkaInc["team_id"], env.kafkaTeam.ID)
	}
}

// TestE2E_TwoTeams_CrossTeamAccessIsForbidden verifies that a team admin
// cannot read incidents, configs, or members of another team.
func TestE2E_TwoTeams_CrossTeamAccessIsForbidden(t *testing.T) {
	env := newE2EEnv(t)

	// Create one incident per team to have something real to guard
	fireWebhook(t, env, env.kongTeam, "Kong: isolation test", "kong")
	fireWebhook(t, env, env.kafkaTeam, "Kafka: isolation test", "kafka")

	type crossTeamCase struct {
		name     string
		cookie   *http.Cookie
		method   string
		path     string
		wantCode int
	}

	cases := []crossTeamCase{
		// Kong admin accessing Kafka endpoints
		{
			name:     "kong reads kafka incidents",
			cookie:   env.kongCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/incidents", env.kafkaTeam.ID),
			wantCode: http.StatusForbidden,
		},
		{
			name:     "kong reads kafka config",
			cookie:   env.kongCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/config", env.kafkaTeam.ID),
			wantCode: http.StatusForbidden,
		},
		{
			name:     "kong reads kafka members",
			cookie:   env.kongCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/members", env.kafkaTeam.ID),
			wantCode: http.StatusForbidden,
		},
		// Kafka admin accessing Kong endpoints
		{
			name:     "kafka reads kong incidents",
			cookie:   env.kafkaCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/incidents", env.kongTeam.ID),
			wantCode: http.StatusForbidden,
		},
		{
			name:     "kafka reads kong config",
			cookie:   env.kafkaCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID),
			wantCode: http.StatusForbidden,
		},
		{
			name:     "kafka reads kong members",
			cookie:   env.kafkaCookie,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/members", env.kongTeam.ID),
			wantCode: http.StatusForbidden,
		},
		// Unauthenticated requests
		{
			name:     "unauthed reads kong incidents",
			cookie:   nil,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/incidents", env.kongTeam.ID),
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "unauthed reads kafka config",
			cookie:   nil,
			method:   "GET",
			path:     fmt.Sprintf("/api/v1/teams/%s/config", env.kafkaTeam.ID),
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			resp := env.do(req)
			if resp.Code != tc.wantCode {
				t.Errorf("got %d, want %d\nbody: %s", resp.Code, tc.wantCode, resp.Body)
			}
		})
	}
}

// TestE2E_TwoTeams_ConfigIsolation verifies that team config is write-isolated:
// one team cannot read or modify the other's data sources or Slack settings.
func TestE2E_TwoTeams_ConfigIsolation(t *testing.T) {
	env := newE2EEnv(t)

	// Kong admin sets Kong's Slack channel
	req := httptest.NewRequest("PUT",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID),
		e2eBody(t, map[string]any{
			"slack_channel":       "#kong-alerts",
			"prometheus_endpoint": "http://prometheus.example.com:9090",
		}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.kongCookie)
	resp := env.do(req)
	if resp.Code != http.StatusOK {
		t.Fatalf("kong set config: got %d, want 200\nbody: %s", resp.Code, resp.Body)
	}

	// Kong admin can read the config back
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID), nil)
	req.AddCookie(env.kongCookie)
	resp = env.do(req)
	if resp.Code != http.StatusOK {
		t.Fatalf("kong get config: got %d, want 200", resp.Code)
	}
	var cfg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	if cfg["slack_channel"] != "#kong-alerts" {
		t.Errorf("kong config slack_channel: got %v, want #kong-alerts", cfg["slack_channel"])
	}
	if cfg["prometheus_endpoint"] != "http://prometheus.example.com:9090" {
		t.Errorf("kong config prometheus_endpoint: got %v, want http://prometheus.example.com:9090",
			cfg["prometheus_endpoint"])
	}
	// webhook_secret must be present so the UI can build the full webhook URL
	if cfg["webhook_secret"] == "" || cfg["webhook_secret"] == nil {
		t.Error("kong config response missing webhook_secret")
	}

	// Kafka admin cannot modify Kong's config
	req = httptest.NewRequest("PUT",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID),
		e2eBody(t, map[string]any{"slack_channel": "#hacked-by-kafka"}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.kafkaCookie)
	resp = env.do(req)
	if resp.Code != http.StatusForbidden {
		t.Errorf("kafka modifying kong config: got %d, want 403", resp.Code)
	}

	// Verify Kong config was NOT changed by the failed attack
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID), nil)
	req.AddCookie(env.kongCookie)
	resp = env.do(req)
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	if cfg["slack_channel"] != "#kong-alerts" {
		t.Errorf("kong config was tampered with — got %v, want #kong-alerts", cfg["slack_channel"])
	}

	// Kafka sets its own config — must not affect Kong
	req = httptest.NewRequest("PUT",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kafkaTeam.ID),
		e2eBody(t, map[string]any{
			"slack_channel": "#kafka-alerts",
			"loki_endpoint": "http://loki.example.com:3100",
		}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.kafkaCookie)
	resp = env.do(req)
	if resp.Code != http.StatusOK {
		t.Fatalf("kafka set config: got %d, want 200\nbody: %s", resp.Code, resp.Body)
	}

	// Kong config still unchanged
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/config", env.kongTeam.ID), nil)
	req.AddCookie(env.kongCookie)
	resp = env.do(req)
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	if cfg["slack_channel"] != "#kong-alerts" {
		t.Errorf("kong config changed after kafka update — got %v, want #kong-alerts", cfg["slack_channel"])
	}
	// Kong should NOT have Kafka's loki endpoint
	if cfg["loki_endpoint"] != nil && cfg["loki_endpoint"] != "" {
		t.Errorf("kong config has loki_endpoint it shouldn't have: %v", cfg["loki_endpoint"])
	}
}

// TestE2E_TwoTeams_MembersIsolation verifies the members endpoint is
// team-scoped: each team can list its own members but is forbidden from
// listing another team's members.
func TestE2E_TwoTeams_MembersIsolation(t *testing.T) {
	env := newE2EEnv(t)

	// Kong admin can list Kong's members (may be empty — that's fine)
	req := httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/members", env.kongTeam.ID), nil)
	req.AddCookie(env.kongCookie)
	resp := env.do(req)
	if resp.Code != http.StatusOK {
		t.Fatalf("kong list own members: got %d, want 200\nbody: %s", resp.Code, resp.Body)
	}
	var kongMembersResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&kongMembersResp)
	if _, ok := kongMembersResp["members"]; !ok {
		t.Error("kong members response missing 'members' field")
	}

	// Kafka admin can list Kafka's members
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/members", env.kafkaTeam.ID), nil)
	req.AddCookie(env.kafkaCookie)
	resp = env.do(req)
	if resp.Code != http.StatusOK {
		t.Fatalf("kafka list own members: got %d, want 200\nbody: %s", resp.Code, resp.Body)
	}

	// Kong admin cannot list Kafka's members
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/members", env.kafkaTeam.ID), nil)
	req.AddCookie(env.kongCookie)
	resp = env.do(req)
	if resp.Code != http.StatusForbidden {
		t.Errorf("kong listing kafka members: got %d, want 403", resp.Code)
	}

	// Kafka admin cannot list Kong's members
	req = httptest.NewRequest("GET",
		fmt.Sprintf("/api/v1/teams/%s/members", env.kongTeam.ID), nil)
	req.AddCookie(env.kafkaCookie)
	resp = env.do(req)
	if resp.Code != http.StatusForbidden {
		t.Errorf("kafka listing kong members: got %d, want 403", resp.Code)
	}
}

// TestE2E_WebhookSecurity verifies that the webhook endpoint rejects requests
// with invalid, swapped, or missing secrets.
func TestE2E_WebhookSecurity(t *testing.T) {
	env := newE2EEnv(t)

	alertPayload := e2eBody(t, map[string]any{
		"title": "Security test alert",
		"state": "alerting",
	})
	alertBytes := alertPayload.Bytes()

	cases := []struct {
		name     string
		teamID   string
		secret   string
		wantCode int
	}{
		{
			name:     "valid kong webhook",
			teamID:   env.kongTeam.ID.String(),
			secret:   env.kongTeam.WebhookSecret,
			wantCode: http.StatusAccepted,
		},
		{
			name:     "valid kafka webhook",
			teamID:   env.kafkaTeam.ID.String(),
			secret:   env.kafkaTeam.WebhookSecret,
			wantCode: http.StatusAccepted,
		},
		{
			name:     "kong team ID with kafka secret",
			teamID:   env.kongTeam.ID.String(),
			secret:   env.kafkaTeam.WebhookSecret,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "kafka team ID with kong secret",
			teamID:   env.kafkaTeam.ID.String(),
			secret:   env.kongTeam.WebhookSecret,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "completely invalid secret",
			teamID:   env.kongTeam.ID.String(),
			secret:   "invalid_secret_abc123",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "invalid team ID",
			teamID:   "not-a-uuid",
			secret:   env.kongTeam.WebhookSecret,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "non-existent team ID",
			teamID:   uuid.New().String(),
			secret:   "does_not_exist",
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/api/v1/webhook/%s/%s", tc.teamID, tc.secret),
				bytes.NewBuffer(alertBytes))
			req.Header.Set("Content-Type", "application/json")
			resp := env.do(req)
			if resp.Code != tc.wantCode {
				t.Errorf("got %d, want %d\nbody: %s", resp.Code, tc.wantCode, resp.Body)
			}
		})
	}
}
