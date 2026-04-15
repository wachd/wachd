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
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	sessionTTL    = 24 * time.Hour
	sessionPrefix = "wachd:session:"
)

// Session holds the data stored in Redis for an authenticated user.
type Session struct {
	IdentityID *uuid.UUID        `json:"identity_id,omitempty"` // nil for local-auth sessions
	Email      string            `json:"email"`
	Name       string            `json:"name"`
	AvatarURL  string            `json:"avatar_url,omitempty"`
	TeamIDs    []uuid.UUID       `json:"team_ids"`
	Roles      map[string]string `json:"roles"` // team_id → role
	ExpiresAt  time.Time         `json:"expires_at"`

	// Enterprise auth additions — zero values are backward-compatible with existing sessions.
	AuthType            string     `json:"auth_type,omitempty"`             // "sso" | "local"
	IsSuperAdmin        bool       `json:"is_superadmin,omitempty"`
	ForcePasswordChange bool       `json:"force_password_change,omitempty"`
	LocalUserID         *uuid.UUID `json:"local_user_id,omitempty"`
}

// SessionStore manages sessions in Redis.
type SessionStore struct {
	client *redis.Client
}

// NewSessionStore creates a SessionStore from a redis.Client.
func NewSessionStore(client *redis.Client) *SessionStore {
	return &SessionStore{client: client}
}

// Create generates a new session token, stores the session in Redis, and returns
// the raw token (which the caller sets as a cookie).
func (s *SessionStore) Create(ctx context.Context, sess *Session) (string, error) {
	// Generate 32 cryptographically random bytes as the raw token
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	sess.ExpiresAt = time.Now().Add(sessionTTL)

	data, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}

	key := sessionPrefix + hashToken(token)
	if err := s.client.Set(ctx, key, data, sessionTTL).Err(); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}

	return token, nil
}

// Get retrieves and deserialises a session by raw token.
// Returns (nil, nil) when the session does not exist or has expired.
func (s *SessionStore) Get(ctx context.Context, token string) (*Session, error) {
	key := sessionPrefix + hashToken(token)
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

// Delete removes a session (on logout).
func (s *SessionStore) Delete(ctx context.Context, token string) error {
	key := sessionPrefix + hashToken(token)
	return s.client.Del(ctx, key).Err()
}

// TokenHash returns the SHA-256 hex of a raw token (used for DB audit trail).
func TokenHash(token string) string {
	return hashToken(token)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// RefreshSession applies fn to the stored session and re-saves it with the
// remaining TTL. Used after password change to clear force_password_change.
// Silently returns nil if the session is missing or expired.
func (s *SessionStore) RefreshSession(ctx context.Context, token string, fn func(*Session)) error {
	key := sessionPrefix + hashToken(token)

	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("get session TTL: %w", err)
	}
	if ttl <= 0 {
		return nil // expired or gone
	}

	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("unmarshal session: %w", err)
	}

	fn(&sess)

	updated, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	return s.client.Set(ctx, key, updated, ttl).Err()
}
