package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wachd/wachd/internal/auth"
	"github.com/wachd/wachd/internal/store"
)

func putConfig(s *Server, team uuid.UUID, role string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body))
	req = mux.SetURLVars(req, map[string]string{"teamId": team.String()})
	sess := &auth.Session{TeamIDs: []uuid.UUID{team}, Roles: map[string]string{team.String(): role}}
	req = req.WithContext(auth.ContextWithSession(req.Context(), sess))
	rec := httptest.NewRecorder()
	s.handleUpsertTeamConfig(rec, req)
	return rec
}

func getConfig(s *Server, team uuid.UUID) map[string]any {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = mux.SetURLVars(req, map[string]string{"teamId": team.String()})
	sess := &auth.Session{TeamIDs: []uuid.UUID{team}, Roles: map[string]string{team.String(): "admin"}}
	req = req.WithContext(auth.ContextWithSession(req.Context(), sess))
	rec := httptest.NewRecorder()
	s.handleGetTeamConfig(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

// Invalid keys are rejected before anything is loaded or stored.
func TestUpsertTeamConfig_RejectsInvalidLokiLabelKey(t *testing.T) {
	team := uuid.New()
	s := &Server{} // no database: validation must fail first
	for _, key := range []string{`app"} or {x="`, "app}", `a="b"`, "9app", "app name", "app.kubernetes.io/name", strings.Repeat("a", 254)} {
		body, _ := json.Marshal(map[string]string{"loki_label_key": key})
		if rec := putConfig(s, team, "admin", string(body)); rec.Code != http.StatusBadRequest {
			t.Errorf("key %q: status %d, want 400", key, rec.Code)
		}
	}
}

func TestUpsertTeamConfig_LokiLabelKeyRequiresAdmin(t *testing.T) {
	team := uuid.New()
	for _, role := range []string{"viewer", "responder"} {
		if rec := putConfig(&Server{}, team, role, `{"loki_label_key":"app"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", role, rec.Code)
		}
	}
}

// Save, read back, change, clear. Existing configs without the key keep working.
func TestTeamConfig_LokiLabelKeyRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wachd:wachd_dev_password@localhost:5432/wachd"
	}
	db, err := store.NewDB(dsn)
	if err != nil {
		t.Skipf("skipping integration test: DB unavailable (%v)", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	team, err := db.CreateTeam(ctx, "loki-"+uuid.NewString()[:8], "sec-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.DeleteTeam(ctx, team.ID) }()
	s := &Server{cfg: db, db: db}

	// Backwards compatibility: a config saved without the field has no key.
	if rec := putConfig(s, team.ID, "admin", `{"slack_channel":"#ops"}`); rec.Code != http.StatusOK {
		t.Fatalf("legacy save: %d %s", rec.Code, rec.Body)
	}
	if _, present := getConfig(s, team.ID)["loki_label_key"]; present {
		t.Error("an unconfigured team must not report a key (default stays service)")
	}

	for _, key := range []string{"app", "app_kubernetes_io_name"} {
		if rec := putConfig(s, team.ID, "admin", `{"loki_label_key":"`+key+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("save %q: %d %s", key, rec.Code, rec.Body)
		}
		if got := getConfig(s, team.ID)["loki_label_key"]; got != key {
			t.Errorf("read back %v, want %q", got, key)
		}
	}
	// Other settings sent later do not wipe the key.
	if rec := putConfig(s, team.ID, "admin", `{"slack_channel":"#ops2"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if got := getConfig(s, team.ID)["loki_label_key"]; got != "app_kubernetes_io_name" {
		t.Errorf("key lost by an unrelated update: %v", got)
	}
	// An empty value restores the default.
	if rec := putConfig(s, team.ID, "admin", `{"loki_label_key":""}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if _, present := getConfig(s, team.ID)["loki_label_key"]; present {
		t.Error("an empty key should clear the setting")
	}
}
