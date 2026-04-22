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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/wachd/wachd/internal/license"
	"github.com/wachd/wachd/internal/store"
)

// AdminHandlers groups all superadmin-only HTTP handlers with their dependencies.
type AdminHandlers struct {
	db      *store.DB
	enc     *Encryptor
	cache   *ProviderCache
	license *license.License
}

// NewAdminHandlers creates the admin handler bundle.
func NewAdminHandlers(db *store.DB, enc *Encryptor, cache *ProviderCache, lic *license.License) *AdminHandlers {
	return &AdminHandlers{db: db, enc: enc, cache: cache, license: lic}
}

// ── Local Users ───────────────────────────────────────────────────────────────

// HandleListUsers returns all local users (passwords excluded).
func (h *AdminHandlers) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListLocalUsers(r.Context())
	if err != nil {
		log.Printf("admin: list users: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if users == nil {
		users = []store.LocalUser{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": sanitizeUsers(users), "count": len(users)})
}

// HandleCreateUser creates a new local user.
func (h *AdminHandlers) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Username    string `json:"username"`
		Email       string `json:"email"`
		Name        string `json:"name"`
		Password    string `json:"password"`
		IsSuperAdmin bool  `json:"is_superadmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Username == "" || req.Email == "" || req.Name == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username, email, name, and password are required"})
		return
	}

	// Enforce license user limit.
	userCount, err := h.db.CountLocalUsers(ctx)
	if err != nil {
		log.Printf("admin: count users: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if userCount >= h.license.MaxUsers {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "user limit reached",
			"limit": h.license.MaxUsers,
			"tier":  string(h.license.Tier),
			"upgrade_url": "https://wachd.io/pricing",
		})
		return
	}

	policy, err := h.db.GetPasswordPolicy(ctx)
	if err != nil {
		log.Printf("admin: get password policy: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if err := ValidatePolicy(req.Password, policy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		log.Printf("admin: hash password: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	user, err := h.db.CreateLocalUser(ctx, req.Username, req.Email, req.Name, hash, req.IsSuperAdmin, true)
	if err != nil {
		log.Printf("admin: create user: %v", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username or email already in use"})
		return
	}
	writeJSON(w, http.StatusCreated, sanitizeUser(user))
}

// HandleGetUser returns a single local user by ID.
func (h *AdminHandlers) HandleGetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	user, err := h.db.GetLocalUserByID(r.Context(), id)
	if err != nil || user == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, sanitizeUser(user))
}

// HandleUpdateUser partially updates a local user (email, name, is_active).
func (h *AdminHandlers) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Email    *string `json:"email"`
		Name     *string `json:"name"`
		IsActive *bool   `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	user, err := h.db.UpdateLocalUser(r.Context(), id, store.LocalUserUpdate{
		Email:    req.Email,
		Name:     req.Name,
		IsActive: req.IsActive,
	})
	if err != nil {
		log.Printf("admin: update user: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, sanitizeUser(user))
}

// HandleDeleteUser removes a local user.
func (h *AdminHandlers) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteLocalUser(r.Context(), id); err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleResetPassword sets a new password for a user and flags force_password_change.
func (h *AdminHandlers) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new_password required"})
		return
	}

	policy, err := h.db.GetPasswordPolicy(ctx)
	if err != nil {
		log.Printf("admin: get password policy: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if err := ValidatePolicy(req.NewPassword, policy); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if err := h.db.UpdatePasswordHash(ctx, id, hash, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
}

// ── Local Groups ──────────────────────────────────────────────────────────────

func (h *AdminHandlers) HandleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.db.ListLocalGroups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if groups == nil {
		groups = []store.LocalGroup{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups, "count": len(groups)})
}

func (h *AdminHandlers) HandleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	group, err := h.db.CreateLocalGroup(r.Context(), req.Name, req.Description)
	if err != nil {
		log.Printf("admin: create group: %v", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "group name already in use"})
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

func (h *AdminHandlers) HandleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteLocalGroup(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandlers) HandleListGroupMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	members, err := h.db.ListGroupMembers(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if members == nil {
		members = []store.LocalUser{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": sanitizeUsers(members), "count": len(members)})
}

func (h *AdminHandlers) HandleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user_id"})
		return
	}
	if err := h.db.AddGroupMember(r.Context(), groupID, userID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (h *AdminHandlers) HandleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	userID, ok := parseUUID(w, r, "userId")
	if !ok {
		return
	}
	if err := h.db.RemoveGroupMember(r.Context(), groupID, userID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandlers) HandleListGroupAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	access, err := h.db.ListGroupAccess(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if access == nil {
		access = []store.TeamAccess{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"access": access, "count": len(access)})
}

func (h *AdminHandlers) HandleGrantGroupAccess(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		TeamID string `json:"team_id"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TeamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "team_id required"})
		return
	}
	teamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid team_id"})
		return
	}
	role := req.Role
	if role == "" {
		role = "viewer"
	}
	if role != "viewer" && role != "responder" && role != "admin" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be viewer, responder, or admin"})
		return
	}
	// Verify the team exists before writing the FK row
	team, err := h.db.GetTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("admin: grant group access — get team: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if team == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "team not found"})
		return
	}
	if err := h.db.GrantGroupAccess(r.Context(), groupID, teamID, role); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "granted"})
}

func (h *AdminHandlers) HandleRevokeGroupAccess(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	teamID, ok := parseUUID(w, r, "teamId")
	if !ok {
		return
	}
	if err := h.db.RevokeGroupAccess(r.Context(), groupID, teamID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── SSO Providers ─────────────────────────────────────────────────────────────

func (h *AdminHandlers) HandleListSSOProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.db.ListSSOProviders(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if providers == nil {
		providers = []store.SSOProvider{}
	}
	public := make([]store.SSOProviderPublic, len(providers))
	for i, p := range providers {
		public[i] = toPublicProvider(p)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": public, "count": len(public)})
}

func (h *AdminHandlers) HandleCreateSSOProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Name          string   `json:"name"`
		ProviderType  string   `json:"provider_type"`
		IssuerURL     string   `json:"issuer_url"`
		ClientID      string   `json:"client_id"`
		ClientSecret  string   `json:"client_secret"`
		Scopes        []string `json:"scopes"`
		Enabled       bool     `json:"enabled"`
		AutoProvision bool     `json:"auto_provision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name == "" || req.IssuerURL == "" || req.ClientID == "" || req.ClientSecret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, issuer_url, client_id, and client_secret are required"})
		return
	}
	if req.ProviderType == "" {
		req.ProviderType = "oidc"
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"openid", "profile", "email"}
	}

	enc, err := h.enc.Encrypt(req.ClientSecret)
	if err != nil {
		log.Printf("admin: encrypt client secret: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	p, err := h.db.CreateSSOProvider(ctx, store.SSOProviderInput{
		Name:            req.Name,
		ProviderType:    req.ProviderType,
		IssuerURL:       req.IssuerURL,
		ClientID:        req.ClientID,
		ClientSecretEnc: enc,
		Scopes:          req.Scopes,
		Enabled:         req.Enabled,
		AutoProvision:   req.AutoProvision,
	})
	if err != nil {
		log.Printf("admin: create sso provider: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, toPublicProvider(*p))
}

func (h *AdminHandlers) HandleGetSSOProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	p, err := h.db.GetSSOProvider(r.Context(), id)
	if err != nil || p == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, toPublicProvider(*p))
}

func (h *AdminHandlers) HandleUpdateSSOProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name          *string  `json:"name"`
		IssuerURL     *string  `json:"issuer_url"`
		ClientID      *string  `json:"client_id"`
		ClientSecret  *string  `json:"client_secret"` // plaintext — encrypted below
		Scopes        []string `json:"scopes"`
		Enabled       *bool    `json:"enabled"`
		AutoProvision *bool    `json:"auto_provision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	u := store.SSOProviderUpdate{
		Name:          req.Name,
		IssuerURL:     req.IssuerURL,
		ClientID:      req.ClientID,
		Scopes:        req.Scopes,
		Enabled:       req.Enabled,
		AutoProvision: req.AutoProvision,
	}
	if req.ClientSecret != nil {
		enc, err := h.enc.Encrypt(*req.ClientSecret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		u.ClientSecretEnc = &enc
	}

	p, err := h.db.UpdateSSOProvider(ctx, id, u)
	if err != nil {
		log.Printf("admin: update sso provider: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	h.cache.Invalidate(id)
	writeJSON(w, http.StatusOK, toPublicProvider(*p))
}

func (h *AdminHandlers) HandleDeleteSSOProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteSSOProvider(r.Context(), id); err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	h.cache.Invalidate(id)
	w.WriteHeader(http.StatusNoContent)
}

// HandleTestSSOProvider attempts OIDC discovery for the provider to verify config.
func (h *AdminHandlers) HandleTestSSOProvider(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	h.cache.Invalidate(id) // Force fresh load for test
	_, err := h.cache.Get(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Password Policy ───────────────────────────────────────────────────────────

func (h *AdminHandlers) HandleGetPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := h.db.GetPasswordPolicy(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *AdminHandlers) HandleUpdatePasswordPolicy(w http.ResponseWriter, r *http.Request) {
	var u store.PasswordPolicyUpdate
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	p, err := h.db.UpdatePasswordPolicy(r.Context(), u)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func parseUUID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	raw := mux.Vars(r)[key]
	id, err := uuid.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid " + key})
		return uuid.UUID{}, false
	}
	return id, true
}

// sanitizeUser returns a copy of LocalUser with the password hash zeroed.
func sanitizeUser(u *store.LocalUser) map[string]interface{} {
	return map[string]interface{}{
		"id":                    u.ID,
		"username":              u.Username,
		"email":                 u.Email,
		"name":                  u.Name,
		"is_superadmin":         u.IsSuperAdmin,
		"is_active":             u.IsActive,
		"force_password_change": u.ForcePasswordChange,
		"failed_login_attempts": u.FailedLoginAttempts,
		"locked_until":          u.LockedUntil,
		"last_login_at":         u.LastLoginAt,
		"created_at":            u.CreatedAt,
		"updated_at":            u.UpdatedAt,
	}
}

func sanitizeUsers(users []store.LocalUser) []map[string]interface{} {
	out := make([]map[string]interface{}, len(users))
	for i := range users {
		out[i] = sanitizeUser(&users[i])
	}
	return out
}

// toPublicProvider masks the encrypted client secret behind a boolean flag.
func toPublicProvider(p store.SSOProvider) store.SSOProviderPublic {
	return store.SSOProviderPublic{
		ID:              p.ID,
		Name:            p.Name,
		ProviderType:    p.ProviderType,
		IssuerURL:       p.IssuerURL,
		ClientID:        p.ClientID,
		ClientSecretSet: p.ClientSecretEnc != "",
		Scopes:          p.Scopes,
		Enabled:         p.Enabled,
		AutoProvision:   p.AutoProvision,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

// HandleListTeams returns all teams (for admin dropdowns).
func (h *AdminHandlers) HandleListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.db.ListTeams(r.Context())
	if err != nil {
		log.Printf("admin: list teams: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if teams == nil {
		teams = []store.Team{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"teams": teams, "count": len(teams)})
}

// HandleCreateTeam creates a new team with a random webhook secret.
func (h *AdminHandlers) HandleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}

	// Enforce license team limit.
	teamCount, err := h.db.CountTeams(r.Context())
	if err != nil {
		log.Printf("admin: count teams: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if teamCount >= h.license.MaxTeams {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "team limit reached",
			"limit": h.license.MaxTeams,
			"tier":  string(h.license.Tier),
			"upgrade_url": "https://wachd.io/pricing",
		})
		return
	}

	// Generate a random webhook secret
	secret, err := randomHex(16)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	team, err := h.db.CreateTeam(r.Context(), req.Name, secret)
	if err != nil {
		log.Printf("admin: create team: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

// HandleDeleteTeam deletes a team and all its cascaded data.
func (h *AdminHandlers) HandleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid team ID"})
		return
	}
	if err := h.db.DeleteTeam(r.Context(), teamID); err != nil {
		if err.Error() == "delete team: no rows in result set" || err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "team not found"})
			return
		}
		log.Printf("admin: delete team %s: %v", teamID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *AdminHandlers) HandleListGroupMappings(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.db.ListGroupMappings(r.Context())
	if err != nil {
		log.Printf("admin: list group mappings: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if mappings == nil {
		mappings = []store.GroupMapping{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"mappings": mappings, "count": len(mappings)})
}

// HandleCreateGroupMapping creates a new AD group → team mapping.
func (h *AdminHandlers) HandleCreateGroupMapping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"provider_id"` // UUID of the sso_providers record
		GroupID    string `json:"group_id"`
		GroupName  string `json:"group_name"`
		TeamID     string `json:"team_id"`
		Role       string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.GroupID == "" || req.TeamID == "" || req.ProviderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider_id, group_id, and team_id are required"})
		return
	}
	providerID, err := uuid.Parse(req.ProviderID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid provider_id"})
		return
	}
	teamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid team_id"})
		return
	}
	role := req.Role
	if role == "" {
		role = "viewer"
	}
	if role != "viewer" && role != "responder" && role != "admin" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be viewer, responder, or admin"})
		return
	}
	var groupName *string
	if req.GroupName != "" {
		groupName = &req.GroupName
	}
	mapping, err := h.db.CreateGroupMapping(r.Context(), providerID, req.GroupID, groupName, teamID, role)
	if err != nil {
		log.Printf("admin: create group mapping: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusCreated, mapping)

	// Best-effort: pre-provision existing group members so they appear in on-call
	// schedules without waiting for first login. Never blocks the HTTP response.
	go h.provisionGroupMembers(context.Background(), providerID, req.GroupID)
}

// HandleDeleteGroupMapping removes a group mapping by ID.
func (h *AdminHandlers) HandleDeleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := h.db.DeleteGroupMapping(r.Context(), id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── API Tokens ────────────────────────────────────────────────────────────────

// HandleListTokens returns all API tokens owned by the calling user.
func (h *AdminHandlers) HandleListTokens(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil || sess.LocalUserID == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "local user required"})
		return
	}
	tokens, err := h.db.ListAPITokensByUser(r.Context(), *sess.LocalUserID)
	if err != nil {
		log.Printf("admin: list tokens: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if tokens == nil {
		tokens = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tokens": tokens, "count": len(tokens)})
}

// HandleCreateToken generates a new API token, stores only the SHA-256 hash,
// and returns the raw token ONCE in the response.
func (h *AdminHandlers) HandleCreateToken(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil || sess.LocalUserID == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "local user required"})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(req.Name) > 255 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 255 characters or fewer"})
		return
	}

	// Generate raw token: "wachd_" + 32 random bytes (hex) = 70 chars total
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	rawToken := "wachd_" + hex.EncodeToString(raw)

	// SHA-256 hash for DB storage
	h256 := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h256[:])

	tok, err := h.db.CreateAPIToken(r.Context(), *sess.LocalUserID, req.Name, tokenHash, nil)
	if err != nil {
		log.Printf("admin: create token: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Return the raw token in the response — this is the ONLY time it is shown.
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         tok.ID,
		"name":       tok.Name,
		"created_at": tok.CreatedAt,
		"token":      rawToken, // shown once, never retrievable again
	})
}

// HandleDeleteToken revokes an API token owned by the calling user.
func (h *AdminHandlers) HandleDeleteToken(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil || sess.LocalUserID == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "local user required"})
		return
	}
	id, ok := parseUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteAPIToken(r.Context(), id, *sess.LocalUserID); err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// provisionGroupMembers fetches the current members of an Entra group via the
// Microsoft Graph API and upserts them as SSO identities with the correct team
// access. Runs best-effort in the background — errors are logged, never surfaced.
func (h *AdminHandlers) provisionGroupMembers(ctx context.Context, providerID uuid.UUID, groupID string) {
	p, err := h.db.GetSSOProvider(ctx, providerID)
	if err != nil || p == nil {
		log.Printf("admin: provision members: load provider %s: %v", providerID, err)
		return
	}

	tenantID, ok := TenantIDFromIssuerURL(p.IssuerURL)
	if !ok {
		// Not an Entra provider — no Graph API to call.
		return
	}

	clientSecret, err := h.enc.Decrypt(p.ClientSecretEnc)
	if err != nil {
		log.Printf("admin: provision members: decrypt secret: %v", err)
		return
	}

	members, err := GetGroupMembers(ctx, tenantID, p.ClientID, clientSecret, groupID)
	if err != nil {
		log.Printf("admin: provision members: graph call failed (app needs GroupMember.Read.All permission): %v", err)
		return
	}

	log.Printf("admin: provision members: found %d member(s) in group %s", len(members), groupID)
	provisioned := 0
	for _, m := range members {
		email := m.Mail
		if email == "" {
			email = m.UserPrincipalName
		}
		if m.ID == "" || email == "" {
			log.Printf("admin: provision members: skipping member id=%q name=%q mail=%q upn=%q — missing id or email", m.ID, m.DisplayName, m.Mail, m.UserPrincipalName)
			continue
		}
		identity, err := h.db.UpsertSSOIdentity(ctx, "entra", m.ID, email, m.DisplayName, nil)
		if err != nil {
			log.Printf("admin: provision members: upsert identity %s: %v", email, err)
			continue
		}
		if err := h.db.SyncTeamAccess(ctx, identity.ID, []string{groupID}, providerID); err != nil {
			log.Printf("admin: provision members: sync access for %s: %v", email, err)
			continue
		}
		provisioned++
	}
	log.Printf("admin: provision members: provisioned %d/%d user(s) for group %s", provisioned, len(members), groupID)
}
