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

// White-box integration tests for the auth package.
// These tests require a real Postgres DB and Redis instance.
// Run: make docker-up  then  go test ./internal/auth/...

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"github.com/wachd/wachd/internal/license"
	"github.com/wachd/wachd/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func requireAuthDB(t *testing.T) *store.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wachd:wachd_dev_password@localhost:5432/wachd"
	}
	db, err := store.NewDB(dsn)
	if err != nil {
		t.Skipf("skipping integration test: DB unavailable (%v) — run make docker-up", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func requireAuthSessions(t *testing.T) *SessionStore {
	t.Helper()
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/1"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Skipf("skipping integration test: Redis URL invalid (%v)", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Skipf("skipping integration test: Redis unavailable (%v) — run make docker-up", err)
	}
	t.Cleanup(func() { client.Close() })
	return NewSessionStore(client)
}

// uniqueAuth returns a unique string prefix for test fixtures.
func uniqueAuth(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, uuid.New().String()[:8])
}

// lowCostHash generates a bcrypt hash at cost 4 for test speed.
func lowCostHash(password string) string {
	b, err := bcrypt.GenerateFromPassword([]byte(password), 4)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// testEncryptor returns an Encryptor with a deterministic test key.
func testEncryptor(t *testing.T) *Encryptor {
	t.Helper()
	// 32 bytes = 64 hex chars
	enc, err := NewEncryptor("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

// testLicense returns a license with a high user limit for testing.
func testLicense() *license.License {
	return &license.License{
		Tier:     license.TierEnterprise,
		MaxUsers: 9999,
	}
}

// makeHandlers creates a Handlers bundle with real DB + session store.
func makeHandlers(db *store.DB, sessions *SessionStore) *Handlers {
	return NewHandlers(nil, nil, sessions, db, "http://localhost:3000")
}

// makeAdminHandlers creates an AdminHandlers bundle with real DB.
func makeAdminHandlers(t *testing.T, db *store.DB) *AdminHandlers {
	t.Helper()
	return NewAdminHandlers(db, testEncryptor(t), nil, testLicense())
}

// helper to build mux.Router with {id} var — needed for parseUUID
func routerWithVar(method, path string, handler http.HandlerFunc) (*mux.Router, string) {
	r := mux.NewRouter()
	r.HandleFunc(path, handler).Methods(method)
	return r, path
}

// ── SessionStore ──────────────────────────────────────────────────────────────

func TestSessionStore_CreateGetDelete(t *testing.T) {
	sessions := requireAuthSessions(t)
	ctx := context.Background()

	sess := &Session{
		Email:        "test@example.com",
		Name:         "Test User",
		TeamIDs:      []uuid.UUID{uuid.New()},
		Roles:        map[string]string{},
		AuthType:     "local",
		IsSuperAdmin: false,
	}

	token, err := sessions.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
	if sess.ExpiresAt.IsZero() {
		t.Error("expected ExpiresAt to be set")
	}

	// Get — valid token
	got, err := sessions.Get(ctx, token)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	if got.Email != "test@example.com" {
		t.Errorf("expected email 'test@example.com', got %q", got.Email)
	}

	// Get — non-existent token
	none, err := sessions.Get(ctx, "no-such-token")
	if err != nil {
		t.Fatalf("Get non-existent: %v", err)
	}
	if none != nil {
		t.Error("expected nil for unknown token")
	}

	// Delete
	if err := sessions.Delete(ctx, token); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify gone
	gone, err := sessions.Get(ctx, token)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if gone != nil {
		t.Error("expected nil after delete")
	}
}

func TestSessionStore_RefreshSession(t *testing.T) {
	sessions := requireAuthSessions(t)
	ctx := context.Background()

	sess := &Session{
		Email:               "refresh@example.com",
		Name:                "Refresh User",
		TeamIDs:             []uuid.UUID{},
		Roles:               map[string]string{},
		ForcePasswordChange: true,
	}

	token, err := sessions.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	// RefreshSession — clear ForcePasswordChange
	err = sessions.RefreshSession(ctx, token, func(s *Session) {
		s.ForcePasswordChange = false
	})
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}

	refreshed, _ := sessions.Get(ctx, token)
	if refreshed == nil {
		t.Fatal("expected session to exist after refresh")
	}
	if refreshed.ForcePasswordChange {
		t.Error("expected ForcePasswordChange=false after refresh")
	}

	// RefreshSession on non-existent token — should return nil (silently)
	err = sessions.RefreshSession(ctx, "no-such-token", func(s *Session) {
		s.ForcePasswordChange = false
	})
	if err != nil {
		t.Errorf("RefreshSession non-existent: expected nil, got %v", err)
	}
}

// ── RequireAuth ───────────────────────────────────────────────────────────────

func TestRequireAuth_ValidCookie(t *testing.T) {
	sessions := requireAuthSessions(t)
	ctx := context.Background()

	sess := &Session{Email: "auth@example.com", Name: "Auth User", TeamIDs: []uuid.UUID{}, Roles: map[string]string{}}
	token, _ := sessions.Create(ctx, sess)
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	reached := false
	handler := sessions.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		got := SessionFromContext(r.Context())
		if got == nil {
			t.Error("expected session in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !reached {
		t.Error("handler not reached")
	}
}

func TestRequireAuth_MissingCookie(t *testing.T) {
	sessions := requireAuthSessions(t)

	handler := sessions.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	sessions := requireAuthSessions(t)

	handler := sessions.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "invalid-token"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ── BearerOrCookie ────────────────────────────────────────────────────────────

func TestBearerOrCookie_BearerToken(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()

	hash := lowCostHash("TestPass123!")
	user, err := db.CreateLocalUser(ctx, uniqueAuth("bearer"), uniqueAuth("b")+"@test.example", "Bearer", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	rawToken := uuid.New().String()
	tokenHash := hashToken(rawToken)
	tok, err := db.CreateAPIToken(ctx, user.ID, "test-token", tokenHash, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	_ = tok

	reached := false
	middleware := BearerOrCookie(sessions, db)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		sess := SessionFromContext(r.Context())
		if sess == nil {
			t.Error("expected session in context for bearer token")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Wait briefly for async TouchAPIToken goroutine
	time.Sleep(10 * time.Millisecond)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !reached {
		t.Error("handler not reached with valid bearer token")
	}
}

func TestBearerOrCookie_InvalidBearer(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)

	middleware := BearerOrCookie(sessions, db)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer no-such-token-in-db")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestBearerOrCookie_CookieFallback(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()

	sess := &Session{Email: "cookie@example.com", Name: "Cookie", TeamIDs: []uuid.UUID{}, Roles: map[string]string{}}
	token, _ := sessions.Create(ctx, sess)
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	reached := false
	middleware := BearerOrCookie(sessions, db)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !reached {
		t.Error("handler not reached via cookie fallback")
	}
}

func TestBearerOrCookie_NoAuth(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)

	middleware := BearerOrCookie(sessions, db)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be reached")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// ── HandleLocalLogin ──────────────────────────────────────────────────────────

func TestHandleLocalLogin_InvalidJSON(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	h := makeHandlers(db, sessions)

	req := httptest.NewRequest(http.MethodPost, "/auth/local", strings.NewReader("{bad json"))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleLocalLogin_EmptyFields(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	h := makeHandlers(db, sessions)

	body, _ := json.Marshal(map[string]string{"username": "", "password": ""})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleLocalLogin_UserNotFound(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	h := makeHandlers(db, sessions)

	body, _ := json.Marshal(map[string]string{"username": "no-such-user", "password": "password"})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandleLocalLogin_WrongPassword(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	hash := lowCostHash("CorrectPass123!")
	user, err := db.CreateLocalUser(ctx, uniqueAuth("wrongpw"), uniqueAuth("wpw")+"@test.example", "WPW", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	body, _ := json.Marshal(map[string]string{"username": user.Username, "password": "WrongPass123!"})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandleLocalLogin_LockedAccount(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	hash := lowCostHash("TestPass123!")
	user, err := db.CreateLocalUser(ctx, uniqueAuth("locked"), uniqueAuth("lk")+"@test.example", "LK", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	// Set lock until future time
	lockUntil := time.Now().Add(time.Hour)
	if err := db.IncrementFailedAttempts(ctx, user.ID, &lockUntil); err != nil {
		t.Fatalf("IncrementFailedAttempts: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"username": user.Username, "password": "TestPass123!"})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for locked account, got %d", rr.Code)
	}
	body2 := rr.Body.String()
	if !strings.Contains(body2, "locked") {
		t.Errorf("expected 'locked' in response, got %q", body2)
	}
}

func TestHandleLocalLogin_Success(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	password := "TestPass123!"
	hash := lowCostHash(password)
	user, err := db.CreateLocalUser(ctx, uniqueAuth("loginok"), uniqueAuth("lok")+"@test.example", "LOK", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	body, _ := json.Marshal(map[string]string{"username": user.Username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify session cookie was set
	cookies := rr.Result().Cookies()
	var sessionTok string
	for _, c := range cookies {
		if c.Name == cookieName {
			sessionTok = c.Value
			break
		}
	}
	if sessionTok == "" {
		t.Error("expected session cookie to be set")
	} else {
		// Cleanup session
		t.Cleanup(func() { _ = sessions.Delete(context.Background(), sessionTok) })
	}
}

func TestHandleLocalLogin_ForcePasswordChange(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	password := "TestPass123!"
	hash := lowCostHash(password)
	// Create user with force_password_change=true
	user, err := db.CreateLocalUser(ctx, uniqueAuth("forcechange"), uniqueAuth("fc")+"@test.example", "FC", hash, false, true)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	body, _ := json.Marshal(map[string]string{"username": user.Username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/local", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleLocalLogin(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (force_change does not block login), got %d", rr.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["force_password_change"] != true {
		t.Errorf("expected force_password_change=true in response, got %v", resp)
	}

	// Cleanup session cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == cookieName {
			t.Cleanup(func() { _ = sessions.Delete(context.Background(), c.Value) })
		}
	}
}

// ── HandleChangePassword ──────────────────────────────────────────────────────

func TestHandleChangePassword_Success(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	oldPass := "OldPass1234!"
	hash := lowCostHash(oldPass)
	user, err := db.CreateLocalUser(ctx, uniqueAuth("chgpw"), uniqueAuth("cp")+"@test.example", "CP", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	// Create a session with LocalUserID set
	sess := &Session{
		Email:       user.Email,
		Name:        user.Name,
		TeamIDs:     []uuid.UUID{},
		Roles:       map[string]string{},
		LocalUserID: &user.ID,
	}
	token, _ := sessions.Create(ctx, sess)
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	body, _ := json.Marshal(map[string]string{
		"current_password": oldPass,
		"new_password":     "NewPass4567!",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewReader(body))
	// Inject session into context (bypassing cookie middleware for unit test)
	reqCtx := context.WithValue(req.Context(), sessionContextKey, sess)
	req = req.WithContext(reqCtx)
	rr := httptest.NewRecorder()
	h.HandleChangePassword(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleChangePassword_NoSession(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	h := makeHandlers(db, sessions)

	body, _ := json.Marshal(map[string]string{"current_password": "x", "new_password": "y"})
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.HandleChangePassword(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for no session, got %d", rr.Code)
	}
}

func TestHandleChangePassword_WrongCurrentPassword(t *testing.T) {
	db := requireAuthDB(t)
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	h := makeHandlers(db, sessions)

	hash := lowCostHash("RealPass123!")
	user, err := db.CreateLocalUser(ctx, uniqueAuth("badcp"), uniqueAuth("bcp")+"@test.example", "BCP", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	sess := &Session{Email: user.Email, Name: user.Name, TeamIDs: []uuid.UUID{}, Roles: map[string]string{}, LocalUserID: &user.ID}

	body, _ := json.Marshal(map[string]string{
		"current_password": "WrongPass!",
		"new_password":     "NewPass456!",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewReader(body))
	reqCtx := context.WithValue(req.Context(), sessionContextKey, sess)
	req = req.WithContext(reqCtx)
	rr := httptest.NewRecorder()
	h.HandleChangePassword(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong current password, got %d", rr.Code)
	}
}

// ── AdminHandlers ─────────────────────────────────────────────────────────────

func TestAdminHandlers_Users(t *testing.T) {
	db := requireAuthDB(t)
	ctx := context.Background()
	h := makeAdminHandlers(t, db)

	// HandleListUsers
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	rr := httptest.NewRecorder()
	h.HandleListUsers(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("HandleListUsers: expected 200, got %d", rr.Code)
	}

	// HandleCreateUser — invalid JSON
	badReq := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader("{bad"))
	badRR := httptest.NewRecorder()
	h.HandleCreateUser(badRR, badReq)
	if badRR.Code != http.StatusBadRequest {
		t.Errorf("HandleCreateUser bad JSON: expected 400, got %d", badRR.Code)
	}

	// HandleCreateUser — missing fields
	missingReq := httptest.NewRequest(http.MethodPost, "/admin/users",
		strings.NewReader(`{"username":"","email":"","name":"","password":""}`))
	missingRR := httptest.NewRecorder()
	h.HandleCreateUser(missingRR, missingReq)
	if missingRR.Code != http.StatusBadRequest {
		t.Errorf("HandleCreateUser missing fields: expected 400, got %d", missingRR.Code)
	}

	// HandleCreateUser — success
	username := uniqueAuth("admusr")
	email := uniqueAuth("adm") + "@test.example"
	createBody, _ := json.Marshal(map[string]interface{}{
		"username": username, "email": email,
		"name": "Admin Created", "password": "AdminPass12!",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/admin/users", bytes.NewReader(createBody))
	createRR := httptest.NewRecorder()
	h.HandleCreateUser(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("HandleCreateUser: expected 201, got %d: %s", createRR.Code, createRR.Body.String())
	}

	var created map[string]interface{}
	_ = json.Unmarshal(createRR.Body.Bytes(), &created)
	userID, _ := uuid.Parse(created["id"].(string))
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, userID) })

	// HandleGetUser — success
	r := mux.NewRouter()
	r.HandleFunc("/admin/users/{id}", h.HandleGetUser)
	getReq := httptest.NewRequest(http.MethodGet, "/admin/users/"+userID.String(), nil)
	getRR := httptest.NewRecorder()
	r.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Errorf("HandleGetUser: expected 200, got %d", getRR.Code)
	}

	// HandleGetUser — invalid UUID
	r2 := mux.NewRouter()
	r2.HandleFunc("/admin/users/{id}", h.HandleGetUser)
	badIDReq := httptest.NewRequest(http.MethodGet, "/admin/users/not-a-uuid", nil)
	badIDRR := httptest.NewRecorder()
	r2.ServeHTTP(badIDRR, badIDReq)
	if badIDRR.Code != http.StatusBadRequest {
		t.Errorf("HandleGetUser bad UUID: expected 400, got %d", badIDRR.Code)
	}

	// HandleUpdateUser — success
	r3 := mux.NewRouter()
	r3.HandleFunc("/admin/users/{id}", h.HandleUpdateUser)
	newEmail := uniqueAuth("upd") + "@test.example"
	updateBody, _ := json.Marshal(map[string]string{"email": newEmail})
	updateReq := httptest.NewRequest(http.MethodPut, "/admin/users/"+userID.String(), bytes.NewReader(updateBody))
	updateRR := httptest.NewRecorder()
	r3.ServeHTTP(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Errorf("HandleUpdateUser: expected 200, got %d: %s", updateRR.Code, updateRR.Body.String())
	}

	// HandleResetPassword — success
	r4 := mux.NewRouter()
	r4.HandleFunc("/admin/users/{id}/reset-password", h.HandleResetPassword)
	resetBody, _ := json.Marshal(map[string]string{"new_password": "NewPass4567!"})
	resetReq := httptest.NewRequest(http.MethodPost, "/admin/users/"+userID.String()+"/reset-password", bytes.NewReader(resetBody))
	resetRR := httptest.NewRecorder()
	r4.ServeHTTP(resetRR, resetReq)
	if resetRR.Code != http.StatusOK {
		t.Errorf("HandleResetPassword: expected 200, got %d", resetRR.Code)
	}

	// HandleDeleteUser — success
	r5 := mux.NewRouter()
	r5.HandleFunc("/admin/users/{id}", h.HandleDeleteUser)
	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/users/"+userID.String(), nil)
	deleteRR := httptest.NewRecorder()
	r5.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Errorf("HandleDeleteUser: expected 204, got %d", deleteRR.Code)
	}
}

func TestAdminHandlers_Groups(t *testing.T) {
	db := requireAuthDB(t)
	ctx := context.Background()
	h := makeAdminHandlers(t, db)

	// HandleListGroups
	listReq := httptest.NewRequest(http.MethodGet, "/admin/groups", nil)
	listRR := httptest.NewRecorder()
	h.HandleListGroups(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Errorf("HandleListGroups: expected 200, got %d", listRR.Code)
	}

	// HandleCreateGroup — success
	groupName := uniqueAuth("grp")
	createBody, _ := json.Marshal(map[string]string{"name": groupName, "description": "test"})
	createReq := httptest.NewRequest(http.MethodPost, "/admin/groups", bytes.NewReader(createBody))
	createRR := httptest.NewRecorder()
	h.HandleCreateGroup(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("HandleCreateGroup: expected 201, got %d: %s", createRR.Code, createRR.Body.String())
	}

	var created map[string]interface{}
	_ = json.Unmarshal(createRR.Body.Bytes(), &created)
	groupID, _ := uuid.Parse(created["id"].(string))
	t.Cleanup(func() { _ = db.DeleteLocalGroup(ctx, groupID) })

	// HandleListGroupMembers — empty
	r := mux.NewRouter()
	r.HandleFunc("/admin/groups/{id}/members", h.HandleListGroupMembers)
	membersReq := httptest.NewRequest(http.MethodGet, "/admin/groups/"+groupID.String()+"/members", nil)
	membersRR := httptest.NewRecorder()
	r.ServeHTTP(membersRR, membersReq)
	if membersRR.Code != http.StatusOK {
		t.Errorf("HandleListGroupMembers: expected 200, got %d", membersRR.Code)
	}

	// HandleDeleteGroup — success
	r2 := mux.NewRouter()
	r2.HandleFunc("/admin/groups/{id}", h.HandleDeleteGroup)
	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/groups/"+groupID.String(), nil)
	deleteRR := httptest.NewRecorder()
	r2.ServeHTTP(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusNoContent {
		t.Errorf("HandleDeleteGroup: expected 204, got %d", deleteRR.Code)
	}
}

// ── AdminHandlers — Password Policy ──────────────────────────────────────────

func TestAdminHandlers_PasswordPolicy(t *testing.T) {
	db := requireAuthDB(t)
	h := makeAdminHandlers(t, db)

	// HandleGetPasswordPolicy
	getReq := httptest.NewRequest(http.MethodGet, "/admin/password-policy", nil)
	getRR := httptest.NewRecorder()
	h.HandleGetPasswordPolicy(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Errorf("HandleGetPasswordPolicy: expected 200, got %d", getRR.Code)
	}

	var policy map[string]interface{}
	_ = json.Unmarshal(getRR.Body.Bytes(), &policy)
	if _, ok := policy["min_length"]; !ok {
		t.Error("expected min_length in password policy response")
	}

	// HandleUpdatePasswordPolicy
	updateBody, _ := json.Marshal(map[string]int{"min_length": 10})
	updateReq := httptest.NewRequest(http.MethodPut, "/admin/password-policy", bytes.NewReader(updateBody))
	updateRR := httptest.NewRecorder()
	h.HandleUpdatePasswordPolicy(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Errorf("HandleUpdatePasswordPolicy: expected 200, got %d: %s", updateRR.Code, updateRR.Body.String())
	}
}
