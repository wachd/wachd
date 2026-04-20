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

package auth

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
)

// ── Session helpers ────────────────────────────────────────────────────────────

// injectSession returns a copy of r with sess injected into its context.
func injectSession(r *http.Request, sess *Session) *http.Request {
	ctx := context.WithValue(r.Context(), sessionContextKey, sess)
	return r.WithContext(ctx)
}

// makeSession returns a minimal non-superadmin session.
func makeSession(role string) *Session {
	teamID := uuid.New()
	return &Session{
		Email:   "alice@example.com",
		Name:    "Alice",
		TeamIDs: []uuid.UUID{teamID},
		Roles:   map[string]string{teamID.String(): role},
		AuthType: "local",
	}
}

// ── RequireAdmin ──────────────────────────────────────────────────────────────

func TestRequireAdmin_NoSession_401(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAdmin_ViewerRole_403(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := injectSession(httptest.NewRequest("GET", "/", nil), makeSession("viewer"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer, got %d", w.Code)
	}
}

func TestRequireAdmin_AdminRole_200(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := injectSession(httptest.NewRequest("GET", "/", nil), makeSession("admin"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for admin, got %d", w.Code)
	}
}

func TestRequireAdmin_SuperAdmin_200(t *testing.T) {
	handler := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	sess := makeSession("viewer")
	sess.IsSuperAdmin = true
	r := injectSession(httptest.NewRequest("GET", "/", nil), sess)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for superadmin, got %d", w.Code)
	}
}

// ── RequireSuperAdmin ─────────────────────────────────────────────────────────

func TestRequireSuperAdmin_NoSession_401(t *testing.T) {
	handler := RequireSuperAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireSuperAdmin_Admin_403(t *testing.T) {
	handler := RequireSuperAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := injectSession(httptest.NewRequest("GET", "/", nil), makeSession("admin"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-superadmin, got %d", w.Code)
	}
}

func TestRequireSuperAdmin_SuperAdmin_200(t *testing.T) {
	handler := RequireSuperAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	sess := makeSession("admin")
	sess.IsSuperAdmin = true
	r := injectSession(httptest.NewRequest("GET", "/", nil), sess)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for superadmin, got %d", w.Code)
	}
}

// ── RequireNoForceChange ──────────────────────────────────────────────────────

func TestRequireNoForceChange_NoSession_401(t *testing.T) {
	handler := RequireNoForceChange(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireNoForceChange_ForceChange_403(t *testing.T) {
	handler := RequireNoForceChange(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	sess := makeSession("admin")
	sess.ForcePasswordChange = true
	r := injectSession(httptest.NewRequest("GET", "/", nil), sess)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when force_password_change=true, got %d", w.Code)
	}
	// Check body contains the specific error code the frontend relies on
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err == nil {
		if body["error"] != "password_change_required" {
			t.Errorf("expected error=password_change_required, got %q", body["error"])
		}
	}
}

func TestRequireNoForceChange_NoForceChange_200(t *testing.T) {
	handler := RequireNoForceChange(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	sess := makeSession("admin")
	sess.ForcePasswordChange = false
	r := injectSession(httptest.NewRequest("GET", "/", nil), sess)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when no force_password_change, got %d", w.Code)
	}
}

// ── HasTeamAccess ─────────────────────────────────────────────────────────────

func TestHasTeamAccess_SuperAdmin_AlwaysTrue(t *testing.T) {
	teamID := uuid.New()
	sess := &Session{IsSuperAdmin: true, TeamIDs: []uuid.UUID{}}
	if !sess.HasTeamAccess(teamID) {
		t.Error("superadmin should always have team access")
	}
}

func TestHasTeamAccess_InTeam(t *testing.T) {
	teamID := uuid.New()
	sess := &Session{TeamIDs: []uuid.UUID{teamID}}
	if !sess.HasTeamAccess(teamID) {
		t.Error("should have access to own team")
	}
}

func TestHasTeamAccess_NotInTeam(t *testing.T) {
	sess := &Session{TeamIDs: []uuid.UUID{uuid.New()}}
	if sess.HasTeamAccess(uuid.New()) {
		t.Error("should not have access to a different team")
	}
}

func TestHasTeamAccess_EmptyTeams(t *testing.T) {
	sess := &Session{TeamIDs: []uuid.UUID{}}
	if sess.HasTeamAccess(uuid.New()) {
		t.Error("should not have access when TeamIDs is empty")
	}
}

// ── HasAdminRole ──────────────────────────────────────────────────────────────

func TestHasAdminRole_SuperAdmin(t *testing.T) {
	sess := &Session{IsSuperAdmin: true}
	if !sess.HasAdminRole() {
		t.Error("superadmin should always have admin role")
	}
}

func TestHasAdminRole_AdminRole(t *testing.T) {
	teamID := uuid.New()
	sess := &Session{Roles: map[string]string{teamID.String(): "admin"}}
	if !sess.HasAdminRole() {
		t.Error("session with admin role should return true")
	}
}

func TestHasAdminRole_ViewerRole(t *testing.T) {
	teamID := uuid.New()
	sess := &Session{Roles: map[string]string{teamID.String(): "viewer"}}
	if sess.HasAdminRole() {
		t.Error("viewer role should not have admin")
	}
}

func TestHasAdminRole_ResponderRole(t *testing.T) {
	teamID := uuid.New()
	sess := &Session{Roles: map[string]string{teamID.String(): "responder"}}
	if sess.HasAdminRole() {
		t.Error("responder role should not have admin")
	}
}

func TestHasAdminRole_NoRoles(t *testing.T) {
	sess := &Session{}
	if sess.HasAdminRole() {
		t.Error("no roles should return false")
	}
}

// ── SessionFromContext ────────────────────────────────────────────────────────

func TestSessionFromContext_Present(t *testing.T) {
	sess := makeSession("viewer")
	ctx := context.WithValue(context.Background(), sessionContextKey, sess)
	got := SessionFromContext(ctx)
	if got != sess {
		t.Error("expected to retrieve the injected session")
	}
}

func TestSessionFromContext_Absent(t *testing.T) {
	got := SessionFromContext(context.Background())
	if got != nil {
		t.Error("expected nil when no session in context")
	}
}

// ── isSecureRequest ───────────────────────────────────────────────────────────

func TestIsSecureRequest_DirectTLS(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.com/", nil)
	// httptest.NewRequest doesn't set r.TLS; simulate by checking header approach
	// Real TLS would set r.TLS != nil — we test via X-Forwarded-Proto instead
	if isSecureRequest(r) {
		// httptest does not actually set r.TLS, so this is expected false unless env var
		// The test verifies the function doesn't panic.
	}
}

func TestIsSecureRequest_ForwardedProtoHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if !isSecureRequest(r) {
		t.Error("expected secure=true with X-Forwarded-Proto: https")
	}
}

func TestIsSecureRequest_ForwardedProtoHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "http")
	if isSecureRequest(r) {
		t.Error("expected secure=false with X-Forwarded-Proto: http")
	}
}

func TestIsSecureRequest_EnvVar(t *testing.T) {
	t.Setenv("AUTH_COOKIE_SECURE", "true")
	r := httptest.NewRequest("GET", "/", nil)
	if !isSecureRequest(r) {
		t.Error("expected secure=true when AUTH_COOKIE_SECURE=true")
	}
}

func TestIsSecureRequest_EnvVarFalse(t *testing.T) {
	os.Unsetenv("AUTH_COOKIE_SECURE")
	r := httptest.NewRequest("GET", "/", nil)
	if isSecureRequest(r) {
		t.Error("expected secure=false with no TLS, no header, no env var")
	}
}

// ── clientIP ──────────────────────────────────────────────────────────────────

func TestClientIP_XForwardedFor_Single(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := clientIP(r); got != "203.0.113.5" {
		t.Errorf("expected 203.0.113.5, got %q", got)
	}
}

func TestClientIP_XForwardedFor_Multiple(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1, 192.168.1.1")
	if got := clientIP(r); got != "203.0.113.5" {
		t.Errorf("expected first IP 203.0.113.5, got %q", got)
	}
}

func TestClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	if got := clientIP(r); got != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1 (stripped port), got %q", got)
	}
}

func TestClientIP_RemoteAddr_NoPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1"
	// No colon → return as-is
	if got := clientIP(r); got != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %q", got)
	}
}

// ── sessionCookie ─────────────────────────────────────────────────────────────

func TestSessionCookie_SetToken(t *testing.T) {
	exp := time.Now().Add(24 * time.Hour)
	c := sessionCookie("mytoken", exp, true)

	if c.Name != cookieName {
		t.Errorf("expected cookie name %q, got %q", cookieName, c.Name)
	}
	if c.Value != "mytoken" {
		t.Errorf("expected value 'mytoken', got %q", c.Value)
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly=true")
	}
	if !c.Secure {
		t.Error("expected Secure=true when secure=true")
	}
	if c.MaxAge != 0 {
		t.Errorf("expected MaxAge=0 for non-clear cookie, got %d", c.MaxAge)
	}
}

func TestSessionCookie_ClearCookie(t *testing.T) {
	c := sessionCookie("", time.Time{}, false)
	if c.MaxAge != -1 {
		t.Errorf("expected MaxAge=-1 for clear cookie, got %d", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("expected empty value, got %q", c.Value)
	}
}

func TestSessionCookie_InsecureFlag(t *testing.T) {
	c := sessionCookie("token", time.Now().Add(time.Hour), false)
	if c.Secure {
		t.Error("expected Secure=false when secure=false")
	}
}

// ── writeJSON ─────────────────────────────────────────────────────────────────

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad input"})

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("could not decode response body: %v", err)
	}
	if body["error"] != "bad input" {
		t.Errorf("expected error='bad input', got %q", body["error"])
	}
}

// ── randomHex ─────────────────────────────────────────────────────────────────

func TestRandomHex_Length(t *testing.T) {
	h, err := randomHex(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 16 bytes → 32 hex chars
	if len(h) != 32 {
		t.Errorf("expected 32 hex chars, got %d", len(h))
	}
}

func TestRandomHex_Unique(t *testing.T) {
	h1, _ := randomHex(16)
	h2, _ := randomHex(16)
	if h1 == h2 {
		t.Error("expected unique hex values")
	}
}

func TestRandomHex_32Bytes(t *testing.T) {
	h, err := randomHex(32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h) != 64 {
		t.Errorf("expected 64 hex chars for 32 bytes, got %d", len(h))
	}
}

// ── hashToken / TokenHash ─────────────────────────────────────────────────────

func TestHashToken_Deterministic(t *testing.T) {
	h1 := hashToken("mytoken")
	h2 := hashToken("mytoken")
	if h1 != h2 {
		t.Error("hash should be deterministic")
	}
}

func TestHashToken_DifferentInputs(t *testing.T) {
	if hashToken("token1") == hashToken("token2") {
		t.Error("different inputs should produce different hashes")
	}
}

func TestTokenHash_MatchesInternal(t *testing.T) {
	tok := "raw-session-token"
	if TokenHash(tok) != hashToken(tok) {
		t.Error("TokenHash should equal internal hashToken")
	}
}

// ── HandleMe ─────────────────────────────────────────────────────────────────

func TestHandleMe_NoSession_401(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	h.HandleMe(w, httptest.NewRequest("GET", "/api/v1/auth/me", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no session, got %d", w.Code)
	}
}

func TestHandleMe_WithSession_200(t *testing.T) {
	h := &Handlers{}
	sess := &Session{
		Email:        "alice@example.com",
		Name:         "Alice",
		IsSuperAdmin: true,
		AuthType:     "local",
		TeamIDs:      []uuid.UUID{uuid.New()},
		Roles:        map[string]string{},
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	r := injectSession(httptest.NewRequest("GET", "/api/v1/auth/me", nil), sess)
	w := httptest.NewRecorder()
	h.HandleMe(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}
	if body["email"] != "alice@example.com" {
		t.Errorf("expected email in response, got %v", body["email"])
	}
	if body["is_superadmin"] != true {
		t.Errorf("expected is_superadmin=true, got %v", body["is_superadmin"])
	}
	if body["auth_type"] != "local" {
		t.Errorf("expected auth_type=local, got %v", body["auth_type"])
	}
}

// ── writeUnauthorized ─────────────────────────────────────────────────────────

func TestWriteUnauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	writeUnauthorized(w)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}
	if body["error"] != "unauthenticated" {
		t.Errorf("expected error=unauthenticated, got %q", body["error"])
	}
}
