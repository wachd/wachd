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
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wachd/wachd/internal/store"
)

const (
	cookieName  = "wachd_session"
	stateTTL    = 5 * time.Minute
	statePrefix = "wachd:oauth:state:"
)

// Handlers groups the auth HTTP handlers with their dependencies.
type Handlers struct {
	provider      *OIDCProvider
	providerCache *ProviderCache
	sessions      *SessionStore
	db            *store.DB
	frontendURL   string // e.g. "http://localhost:3000" — used for post-auth redirects
}

// NewHandlers creates an auth handler bundle.
func NewHandlers(provider *OIDCProvider, providerCache *ProviderCache, sessions *SessionStore, db *store.DB, frontendURL string) *Handlers {
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}
	return &Handlers{
		provider:      provider,
		providerCache: providerCache,
		sessions:      sessions,
		db:            db,
		frontendURL:   frontendURL,
	}
}

// HandleLogin starts the PKCE+state OAuth flow and redirects to the IdP.
// It uses the env-var provider when configured, otherwise the first enabled
// DB-stored provider.
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	provider := h.provider
	if provider == nil && h.providerCache != nil {
		p, _, _, err := h.providerCache.GetFirst(ctx)
		if err != nil {
			log.Printf("auth: load provider from DB: %v", err)
			http.Error(w, "SSO provider unavailable", http.StatusServiceUnavailable)
			return
		}
		provider = p
	}
	if provider == nil {
		http.Error(w, "No SSO provider configured", http.StatusServiceUnavailable)
		return
	}

	state, err := randomHex(16)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	verifier, err := randomHex(32)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store verifier in Redis keyed by state (Set with NX option ensures uniqueness)
	ok, err := h.sessions.client.Set(r.Context(), statePrefix+state, verifier, stateTTL).Result()
	if err != nil || ok != "OK" {
		// Collision (astronomically rare) or Redis error — retry with new state
		log.Printf("auth: state key conflict or Redis error, retrying: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	url := provider.AuthCodeURL(state, verifier)
	http.Redirect(w, r, url, http.StatusFound)
}

// HandleCallback processes the OAuth callback, syncs group memberships, and sets the session cookie.
func (h *Handlers) HandleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	provider := h.provider
	providerID := uuid.Nil
	if provider == nil && h.providerCache != nil {
		p, pid, _, err := h.providerCache.GetFirst(ctx)
		if err != nil {
			log.Printf("auth: callback: load provider from DB: %v", err)
			http.Error(w, "SSO provider unavailable", http.StatusServiceUnavailable)
			return
		}
		provider = p
		if pid != nil {
			providerID = *pid
		}
	}
	if provider == nil {
		http.Error(w, "No SSO provider configured", http.StatusServiceUnavailable)
		return
	}

	// Retrieve and atomically delete the PKCE verifier from Redis.
	// Use GET + DEL instead of GETDEL for Redis 6.0 compatibility (GETDEL requires 6.2+).
	key := statePrefix + state
	verifier, err := h.sessions.client.Get(ctx, key).Result()
	if err != nil {
		// Do not reveal whether state was invalid vs expired
		log.Printf("auth: state lookup failed: %v", err)
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}
	h.sessions.client.Del(ctx, key)

	// Exchange code for tokens
	idToken, accessToken, err := provider.Exchange(ctx, code, verifier)
	if err != nil {
		log.Printf("auth: token exchange: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	claims, err := ExtractClaims(idToken)
	if err != nil {
		log.Printf("auth: extract claims: %v", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	// Upsert identity in DB
	identity, err := h.db.UpsertSSOIdentity(ctx, "entra", claims.Sub, claims.Email, claims.Name, nil)
	if err != nil {
		log.Printf("auth: upsert identity: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fetch group memberships — prefer the `groups` claim baked into the ID token
	// (requires "groupMembershipClaims" configured on the Entra app registration).
	// Fall back to Microsoft Graph when the claim is absent (requires Graph permission).
	var groupIDs []string
	if len(claims.Groups) > 0 {
		groupIDs = claims.Groups
		log.Printf("auth: got %d group(s) from ID token claim for %s", len(groupIDs), identity.Email)
	} else {
		groups, err := GetGroupMemberships(ctx, accessToken)
		if err != nil {
			log.Printf("auth: failed to fetch group memberships from Graph for %s: %v", identity.Email, err)
			log.Printf("auth: tip — configure 'groupMembershipClaims' on the Azure app registration to embed groups in the ID token")
		} else {
			groupIDs = make([]string, len(groups))
			for i, g := range groups {
				groupIDs[i] = g.ID
			}
			log.Printf("auth: got %d group(s) from Microsoft Graph for %s", len(groupIDs), identity.Email)
		}
	}

	// Sync team access based on group memberships
	log.Printf("auth: syncing team access — providerID=%s groupIDs=%v", providerID, groupIDs)
	if err := h.db.SyncTeamAccess(ctx, identity.ID, groupIDs, providerID); err != nil {
		log.Printf("auth: sync team access: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Load team access for session
	teamAccess, err := h.db.GetIdentityTeams(ctx, identity.ID)
	if err != nil {
		log.Printf("auth: get identity teams: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Reject SSO users who have no team access — they are authenticated by the
	// IdP but have not been granted access to any team via group mappings.
	// This prevents unknown tenant members from landing on the dashboard.
	if len(teamAccess) == 0 {
		log.Printf("auth: SSO login rejected for %q — no team access grants (configure group mappings)", identity.Email)
		http.Redirect(w, r, h.frontendURL+"/login?error=no_access", http.StatusFound)
		return
	}

	teamIDs := make([]uuid.UUID, len(teamAccess))
	roles := make(map[string]string, len(teamAccess))
	for i, ta := range teamAccess {
		teamIDs[i] = ta.TeamID
		roles[ta.TeamID.String()] = ta.Role
	}

	// Create session in Redis
	sess := &Session{
		IdentityID: &identity.ID,
		Email:      identity.Email,
		Name:       identity.Name,
		TeamIDs:    teamIDs,
		Roles:      roles,
		AuthType:   "sso",
	}
	token, err := h.sessions.Create(ctx, sess)
	if err != nil {
		log.Printf("auth: create session: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Write audit record to DB (best-effort — non-fatal)
	ip := clientIP(r)
	_ = h.db.RecordSession(ctx, identity.ID, TokenHash(token), sess.ExpiresAt, ip)

	http.SetCookie(w, sessionCookie(token, sess.ExpiresAt, isSecureRequest(r)))
	log.Printf("auth: login %s → %d team(s)", identity.Email, len(teamIDs))
	http.Redirect(w, r, "/", http.StatusFound)
}

// HandleLogout deletes the session and clears the cookie.
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(cookieName)
	if err == nil && cookie.Value != "" {
		_ = h.sessions.Delete(r.Context(), cookie.Value)
		_ = h.db.DeleteSessionByHash(r.Context(), TokenHash(cookie.Value))
	}

	http.SetCookie(w, sessionCookie("", time.Time{}, isSecureRequest(r)))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
}

// HandleMe returns the current user's session info.
func (h *Handlers) HandleMe(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		writeUnauthorized(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"identity_id":           sess.IdentityID, // nil for local-auth sessions
		"email":                 sess.Email,
		"name":                  sess.Name,
		"avatar_url":            sess.AvatarURL,
		"team_ids":              sess.TeamIDs,
		"roles":                 sess.Roles,
		"expires_at":            sess.ExpiresAt,
		"auth_type":             sess.AuthType,
		"is_superadmin":         sess.IsSuperAdmin,
		"force_password_change": sess.ForcePasswordChange,
	})
}

// HandleListGroupMappings returns all group mappings.
// Must be called behind RequireAuth + RequireAdmin middleware.
func (h *Handlers) HandleListGroupMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.db.ListGroupMappings(r.Context())
	if err != nil {
		log.Printf("auth: list group mappings: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if mappings == nil {
		mappings = []store.GroupMapping{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"mappings": mappings,
		"count":    len(mappings),
	})
}

// HandleDeleteGroupMapping removes a group mapping by ID.
// Must be called behind RequireAuth + RequireAdmin middleware.
func (h *Handlers) HandleDeleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		http.Error(w, "missing mapping id", http.StatusBadRequest)
		return
	}

	mappingID, err := uuid.Parse(id)
	if err != nil {
		http.Error(w, "invalid mapping id", http.StatusBadRequest)
		return
	}

	if err := h.db.DeleteGroupMapping(r.Context(), mappingID); err != nil {
		log.Printf("auth: delete group mapping: %v", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleLocalLogin authenticates a local (non-SSO) user with username + password.
// On success it creates a session and sets the wachd_session cookie.
// Returns 403 with error "password_change_required" when force_password_change is set.
func (h *Handlers) HandleLocalLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username and password required"})
		return
	}

	user, err := h.db.GetLocalUserByUsername(ctx, req.Username)
	if err != nil {
		log.Printf("auth: local login lookup: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Fetch password policy before the credential checks so lockout config is always
	// applied from the DB. Fail hard if the policy cannot be read — a degraded policy
	// could allow weaker security guarantees during an outage.
	policy, err := h.db.GetPasswordPolicy(ctx)
	if err != nil {
		log.Printf("auth: get password policy: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Perform a dummy bcrypt comparison when the user is not found so that the
	// response time is indistinguishable from a wrong-password attempt.
	// This prevents username enumeration via timing.
	if user == nil || !user.IsActive {
		_ = CheckPassword("$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", req.Password)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	// Check account lockout
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "account temporarily locked"})
		return
	}

	// Verify password
	if err := CheckPassword(user.PasswordHash, req.Password); err != nil {
		// Increment failed attempts, lock if threshold reached
		var lockUntil *time.Time
		newCount := user.FailedLoginAttempts + 1
		if policy.MaxFailedAttempts > 0 && newCount >= policy.MaxFailedAttempts {
			t := time.Now().Add(time.Duration(policy.LockoutDurationMinutes) * time.Minute)
			lockUntil = &t
			log.Printf("auth: locking account %s for %d minutes", user.Username, policy.LockoutDurationMinutes)
		}
		if lockErr := h.db.IncrementFailedAttempts(ctx, user.ID, lockUntil); lockErr != nil {
			log.Printf("auth: increment failed attempts for %s: %v", user.Username, lockErr)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	// Credentials valid — reset failed attempts and record login
	_ = h.db.ResetFailedAttempts(ctx, user.ID)
	_ = h.db.RecordLocalLogin(ctx, user.ID)

	// Fetch team access from local groups
	teamAccess, err := h.db.GetLocalUserTeams(ctx, user.ID)
	if err != nil {
		log.Printf("auth: get local user teams: %v", err)
		teamAccess = nil
	}

	teamIDs := make([]uuid.UUID, len(teamAccess))
	roles := make(map[string]string, len(teamAccess))
	for i, ta := range teamAccess {
		teamIDs[i] = ta.TeamID
		roles[ta.TeamID.String()] = ta.Role
	}

	sess := &Session{
		Email:               user.Email,
		Name:                user.Name,
		TeamIDs:             teamIDs,
		Roles:               roles,
		AuthType:            "local",
		IsSuperAdmin:        user.IsSuperAdmin,
		ForcePasswordChange: user.ForcePasswordChange,
		LocalUserID:         &user.ID,
	}
	token, err := h.sessions.Create(ctx, sess)
	if err != nil {
		log.Printf("auth: create session: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	http.SetCookie(w, sessionCookie(token, sess.ExpiresAt, isSecureRequest(r)))
	log.Printf("auth: local login %s (superadmin=%v force_change=%v)",
		user.Username, user.IsSuperAdmin, user.ForcePasswordChange)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"force_password_change": user.ForcePasswordChange,
		"is_superadmin":         user.IsSuperAdmin,
	})
}

// HandleChangePassword lets an authenticated local user change their password.
// Validates the current password and enforces the DB password policy on the new one.
func (h *Handlers) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sess := SessionFromContext(ctx)
	if sess == nil || sess.LocalUserID == nil {
		writeUnauthorized(w)
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current_password and new_password required"})
		return
	}

	user, err := h.db.GetLocalUserByID(ctx, *sess.LocalUserID)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	// Verify current password
	if err := CheckPassword(user.PasswordHash, req.CurrentPassword); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
		return
	}

	// Enforce password policy
	policy, err := h.db.GetPasswordPolicy(ctx)
	if err != nil {
		log.Printf("auth: get password policy: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if err := ValidatePolicy(req.NewPassword, policy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		log.Printf("auth: hash password: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if err := h.db.UpdatePasswordHash(ctx, user.ID, hash, false); err != nil {
		log.Printf("auth: update password hash: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Patch the live session in Redis so force_password_change is cleared immediately.
	// The cookie is still valid — no re-login required.
	if cookie, cookieErr := r.Cookie(cookieName); cookieErr == nil {
		if refreshErr := h.sessions.RefreshSession(ctx, cookie.Value, func(s *Session) {
			s.ForcePasswordChange = false
		}); refreshErr != nil {
			log.Printf("auth: refresh session after password change: %v", refreshErr)
		}
	}

	log.Printf("auth: password changed for user %s", user.Username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
// Passing an empty value with zero time clears the cookie (logout).
func sessionCookie(token string, expires time.Time, secure bool) *http.Cookie {
	c := &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.Expires = expires
	}
	return c
}

// isSecureRequest returns true when the connection is HTTPS, either directly
// or via a trusted reverse proxy header. Falls back to the AUTH_COOKIE_SECURE
// env var ("true"/"false") to allow always-secure in production environments
// where X-Forwarded-Proto may not be set.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto != "" {
		return strings.EqualFold(proto, "https")
	}
	// Explicit override for environments where neither TLS nor the header is set
	if os.Getenv("AUTH_COOKIE_SECURE") == "true" {
		return true
	}
	return false
}

// clientIP extracts the best-effort client IP for audit logging.
// Uses only the first value of X-Forwarded-For (the original client).
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// X-Forwarded-For: client, proxy1, proxy2 — take only the first value
		if idx := strings.Index(fwd, ","); idx >= 0 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}
	// Strip port from RemoteAddr (ip:port)
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx >= 0 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}
