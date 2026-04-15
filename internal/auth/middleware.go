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
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

type contextKey int

const sessionContextKey contextKey = 0

// RequireAuth is an HTTP middleware that validates the session cookie and injects
// the session into the request context. Returns 401 if no valid session is found.
func (s *SessionStore) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			writeUnauthorized(w)
			return
		}

		sess, err := s.Get(r.Context(), cookie.Value)
		if err != nil || sess == nil {
			writeUnauthorized(w)
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is an HTTP middleware that enforces the admin role.
// Must be chained after RequireAuth (session must already be in context).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFromContext(r.Context())
		if sess == nil {
			writeUnauthorized(w)
			return
		}
		if !sess.HasAdminRole() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSuperAdmin is an HTTP middleware that enforces the superadmin flag.
// Must be chained after RequireAuth.
func RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFromContext(r.Context())
		if sess == nil {
			writeUnauthorized(w)
			return
		}
		if !sess.IsSuperAdmin {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireNoForceChange is an HTTP middleware that blocks requests when the
// session has force_password_change set. The frontend intercepts this error
// and redirects the user to /change-password.
// Must be chained after RequireAuth.
func RequireNoForceChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFromContext(r.Context())
		if sess == nil {
			writeUnauthorized(w)
			return
		}
		if sess.ForcePasswordChange {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "password_change_required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HasTeamAccess returns true if the session grants access to the given team.
// Superadmins always pass — they manage all teams.
func (s *Session) HasTeamAccess(teamID uuid.UUID) bool {
	if s.IsSuperAdmin {
		return true
	}
	for _, tid := range s.TeamIDs {
		if tid == teamID {
			return true
		}
	}
	return false
}

// HasAdminRole returns true if the session has the admin role in any team,
// or if the user is a superadmin (superadmin passes all admin gates).
func (s *Session) HasAdminRole() bool {
	if s.IsSuperAdmin {
		return true
	}
	for _, role := range s.Roles {
		if role == "admin" {
			return true
		}
	}
	return false
}

// SessionFromContext retrieves the session from the context, or nil if not present.
func SessionFromContext(ctx context.Context) *Session {
	sess, _ := ctx.Value(sessionContextKey).(*Session)
	return sess
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
}

// BearerOrCookie returns a middleware that accepts either:
//   - Authorization: Bearer <wachd_token>  (API token / PAT)
//   - Session cookie                        (browser / UI)
//
// API token requests bypass force_password_change so CI pipelines are never blocked.
func BearerOrCookie(sessions *SessionStore, db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authHdr := r.Header.Get("Authorization"); strings.HasPrefix(authHdr, "Bearer ") {
				raw := strings.TrimPrefix(authHdr, "Bearer ")
				h := sha256.Sum256([]byte(raw))
				hash := hex.EncodeToString(h[:])

				tok, user, err := db.GetAPITokenWithUser(r.Context(), hash)
				if err != nil {
					log.Printf("auth: bearer token lookup: %v", err)
					writeUnauthorized(w)
					return
				}
				if tok == nil || user == nil {
					writeUnauthorized(w)
					return
				}

				// Constant-time comparison as defence-in-depth against timing oracles.
				// The DB lookup already used the full hash as a WHERE predicate (index-efficient);
				// this re-check ensures the comparison is not short-circuited by the DB layer.
				if subtle.ConstantTimeCompare([]byte(tok.StoredHash), []byte(hash)) != 1 {
					writeUnauthorized(w)
					return
				}

				// Warn if acting via token while password change is required.
				if user.ForcePasswordChange {
					log.Printf("auth: token request from user %s with force_password_change=true — token bypasses interactive requirement", user.Username)
				}

				// Build session from the token owner's profile.
				teamAccess, err := db.GetLocalUserTeams(r.Context(), user.ID)
				if err != nil {
					log.Printf("auth: get token user teams: %v", err)
				}
				teamIDs := make([]uuid.UUID, 0, len(teamAccess))
				roles := make(map[string]string, len(teamAccess))
				for _, ta := range teamAccess {
					teamIDs = append(teamIDs, ta.TeamID)
					roles[ta.TeamID.String()] = ta.Role
				}

				sess := &Session{
					LocalUserID:         &user.ID,
					Email:               user.Email,
					Name:                user.Name,
					IsSuperAdmin:        user.IsSuperAdmin,
					AuthType:            "token",
					ForcePasswordChange: false, // tokens always bypass this
					TeamIDs:             teamIDs,
					Roles:               roles,
				}
				ctx := context.WithValue(r.Context(), sessionContextKey, sess)

				// Update last_used_at asynchronously — don't block the request.
				go func() {
					if terr := db.TouchAPIToken(context.Background(), tok.ID); terr != nil {
						log.Printf("auth: touch api token: %v", terr)
					}
				}()

				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Fall back to cookie-based session.
			cookie, err := r.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				writeUnauthorized(w)
				return
			}
			sess, err := sessions.Get(r.Context(), cookie.Value)
			if err != nil || sess == nil {
				writeUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), sessionContextKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// randomHex generates n random bytes and returns them as a hex string.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
