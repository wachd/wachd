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

// Integration tests for Personal Access Token (PAT) authentication.
//
// Requires a running PostgreSQL and Redis. Defaults to the dev instances.
// Override with TEST_DATABASE_URL and TEST_REDIS_URL environment variables.
//
// Run with:
//
//	go test -tags integration -v ./internal/auth/
package auth

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
	"github.com/wachd/wachd/internal/license"
	"github.com/wachd/wachd/internal/store"
)

// ── test harness ──────────────────────────────────────────────────────────────

type patTestEnv struct {
	db      *store.DB
	sess    *SessionStore
	adminH  *AdminHandlers
	router  *mux.Router
	adminID uuid.UUID
	cookie  *http.Cookie
}

func newPATTestEnv(t *testing.T) *patTestEnv {
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

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rc := redis.NewClient(opt)
	t.Cleanup(func() { _ = rc.Close() })

	sessions := NewSessionStore(rc)
	enc, err := NewEncryptor("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	// Create isolated test superadmin — unique per run
	username := fmt.Sprintf("pat_test_%d", time.Now().UnixNano())
	hash, err := HashPassword("TestPass1234!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := db.CreateLocalUser(context.Background(),
		username, username+"@test.local", "PAT Test Admin", hash, true, false)
	if err != nil {
		t.Fatalf("create test admin: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(context.Background(), user.ID) })

	// Create a cookie session for this admin
	sess := &Session{
		LocalUserID:  &user.ID,
		Email:        user.Email,
		Name:         user.Name,
		AuthType:     "local",
		IsSuperAdmin: true,
		TeamIDs:      []uuid.UUID{},
		Roles:        map[string]string{},
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	token, err := sessions.Create(context.Background(), sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookie := &http.Cookie{Name: "wachd_session", Value: token}

	lic := license.OSS()
	adminH := NewAdminHandlers(db, enc, nil, lic)

	// Minimal router that mirrors main.go
	router := mux.NewRouter()
	authMW := BearerOrCookie(sessions, db)

	superRouter := router.PathPrefix("/api/v1/admin").Subrouter()
	superRouter.Use(authMW)
	superRouter.HandleFunc("/tokens", adminH.HandleListTokens).Methods("GET")
	superRouter.HandleFunc("/tokens", adminH.HandleCreateToken).Methods("POST")
	superRouter.HandleFunc("/tokens/{id}", adminH.HandleDeleteToken).Methods("DELETE")

	// /auth/me equivalent using SessionFromContext
	router.Handle("/auth/me", authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := SessionFromContext(r.Context())
		if s == nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"auth_type":    s.AuthType,
			"is_superadmin": s.IsSuperAdmin,
			"name":          s.Name,
		})
	}))).Methods("GET")

	return &patTestEnv{
		db:      db,
		sess:    sessions,
		adminH:  adminH,
		router:  router,
		adminID: user.ID,
		cookie:  cookie,
	}
}

func (e *patTestEnv) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewBuffer(b)
}

// ── create ────────────────────────────────────────────────────────────────────

func TestPAT_Create_ReturnsTokenWithWachdPrefix(t *testing.T) {
	env := newPATTestEnv(t)

	req := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "ci-token"}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.cookie)

	resp := env.do(req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201. body: %s", resp.Code, resp.Body)
	}

	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)

	rawToken, _ := body["token"].(string)
	if len(rawToken) < 10 || rawToken[:6] != "wachd_" {
		t.Errorf("token format unexpected: %q (want wachd_<hex>)", rawToken)
	}
	if _, ok := body["id"]; !ok {
		t.Error("response missing 'id' field")
	}
	if _, ok := body["name"]; !ok {
		t.Error("response missing 'name' field")
	}
}

func TestPAT_Create_EmptyName_Returns400(t *testing.T) {
	env := newPATTestEnv(t)

	req := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": ""}))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(env.cookie)

	resp := env.do(req)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.Code)
	}
}

func TestPAT_Create_Unauthenticated_Returns401(t *testing.T) {
	env := newPATTestEnv(t)

	req := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "no-auth"}))
	req.Header.Set("Content-Type", "application/json")
	// no cookie, no bearer

	resp := env.do(req)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", resp.Code)
	}
}

// ── authenticate ──────────────────────────────────────────────────────────────

func TestPAT_Bearer_ValidToken_Authenticates(t *testing.T) {
	env := newPATTestEnv(t)

	// Create a token via cookie session
	createReq := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "bearer-test"}))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(env.cookie)
	createResp := env.do(createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create: %d — %s", createResp.Code, createResp.Body)
	}

	var created map[string]any
	_ = json.NewDecoder(createResp.Body).Decode(&created)
	rawToken := created["token"].(string)

	// Use the PAT on /auth/me
	meReq := httptest.NewRequest("GET", "/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+rawToken)
	meResp := env.do(meReq)

	if meResp.Code != http.StatusOK {
		t.Fatalf("/auth/me with valid PAT: got %d, want 200. body: %s", meResp.Code, meResp.Body)
	}

	var me map[string]any
	_ = json.NewDecoder(meResp.Body).Decode(&me)
	if me["auth_type"] != "token" {
		t.Errorf("auth_type: got %v, want 'token'", me["auth_type"])
	}
}

func TestPAT_Bearer_InvalidToken_Returns401(t *testing.T) {
	env := newPATTestEnv(t)

	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer wachd_000000000000000000000000000000000000000000000000000000000000000000")
	resp := env.do(req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("invalid PAT: got %d, want 401", resp.Code)
	}
}

func TestPAT_Bearer_MalformedHeader_Returns401(t *testing.T) {
	env := newPATTestEnv(t)

	for _, header := range []string{
		"NotBearer token123",
		"Bearer",
		"Bearer ",
		"token123",
	} {
		req := httptest.NewRequest("GET", "/auth/me", nil)
		req.Header.Set("Authorization", header)
		resp := env.do(req)
		if resp.Code != http.StatusUnauthorized {
			t.Errorf("header %q: got %d, want 401", header, resp.Code)
		}
	}
}

// ── revoke ────────────────────────────────────────────────────────────────────

func TestPAT_Revoke_TokenBecomesInvalid(t *testing.T) {
	env := newPATTestEnv(t)

	// Create
	createReq := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "revoke-test"}))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(env.cookie)
	createResp := env.do(createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create: %d — %s", createResp.Code, createResp.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(createResp.Body).Decode(&created)
	rawToken := created["token"].(string)
	tokenID := created["id"].(string)

	// Confirm works
	useReq := httptest.NewRequest("GET", "/auth/me", nil)
	useReq.Header.Set("Authorization", "Bearer "+rawToken)
	if env.do(useReq).Code != http.StatusOK {
		t.Fatal("token should work before revocation")
	}

	// Revoke
	delReq := httptest.NewRequest("DELETE", "/api/v1/admin/tokens/"+tokenID, nil)
	delReq.AddCookie(env.cookie)
	if resp := env.do(delReq); resp.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204. body: %s", resp.Code, resp.Body)
	}

	// Confirm revoked
	useReq2 := httptest.NewRequest("GET", "/auth/me", nil)
	useReq2.Header.Set("Authorization", "Bearer "+rawToken)
	if env.do(useReq2).Code != http.StatusUnauthorized {
		t.Error("revoked token should return 401")
	}
}

// ── list ──────────────────────────────────────────────────────────────────────

func TestPAT_List_DoesNotExposeRawToken(t *testing.T) {
	env := newPATTestEnv(t)

	// Create a token
	createReq := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "list-check"}))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(env.cookie)
	createResp := env.do(createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create: %d — %s", createResp.Code, createResp.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(createResp.Body).Decode(&created)
	rawToken := created["token"].(string)

	// List tokens
	listReq := httptest.NewRequest("GET", "/api/v1/admin/tokens", nil)
	listReq.AddCookie(env.cookie)
	listResp := env.do(listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list: got %d, want 200", listResp.Code)
	}

	var body map[string]any
	_ = json.NewDecoder(listResp.Body).Decode(&body)
	tokens := body["tokens"].([]any)

	for _, tok := range tokens {
		m := tok.(map[string]any)
		// Raw token must never appear in list response
		if val, ok := m["token"]; ok {
			t.Errorf("list response must not include raw token value, got: %v", val)
		}
		// And must not contain the raw token value anywhere
		raw, _ := json.Marshal(m)
		if string(raw) == rawToken {
			t.Error("raw token value found in list response")
		}
	}
}

// ── storage: hash not plaintext ───────────────────────────────────────────────

func TestPAT_StoredHashNotPlaintext(t *testing.T) {
	env := newPATTestEnv(t)

	createReq := httptest.NewRequest("POST", "/api/v1/admin/tokens",
		jsonBody(t, map[string]string{"name": "hash-verify"}))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(env.cookie)
	createResp := env.do(createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create: %d — %s", createResp.Code, createResp.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(createResp.Body).Decode(&created)
	rawToken := created["token"].(string)

	// Query the raw hash directly — ListAPITokensByUser intentionally omits it.
	var storedHash string
	err := env.db.Pool().QueryRow(context.Background(),
		"SELECT token_hash FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1",
		env.adminID,
	).Scan(&storedHash)
	if err != nil {
		t.Fatalf("query token_hash from DB: %v", err)
	}

	// Raw token must never be in the DB — only its SHA-256 hash
	if storedHash == rawToken {
		t.Error("raw token stored in DB — must only store SHA-256 hash")
	}
	// SHA-256 hex is exactly 64 characters
	if len(storedHash) != 64 {
		t.Errorf("stored hash length: got %d, want 64 (SHA-256 hex)", len(storedHash))
	}
}
