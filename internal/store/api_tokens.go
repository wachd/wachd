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

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateAPIToken inserts a new API token row. tokenHash is the SHA-256 hex of the
// raw token; the raw token is never stored and must be shown to the user once.
func (db *DB) CreateAPIToken(ctx context.Context, userID uuid.UUID, name, tokenHash string, expiresAt *time.Time) (*APIToken, error) {
	query := `
		INSERT INTO api_tokens (user_id, name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, last_used_at, expires_at, created_at
	`
	row := db.pool.QueryRow(ctx, query, userID, name, tokenHash, expiresAt)
	return scanAPIToken(row)
}

// GetAPITokenWithUser looks up a token by its SHA-256 hash and returns both the
// token record and the owning user in a single JOIN. Returns (nil, nil, nil) when
// the token does not exist or has expired. The returned APIToken includes the stored
// hash so the caller can perform a constant-time comparison as defence-in-depth.
func (db *DB) GetAPITokenWithUser(ctx context.Context, tokenHash string) (*APIToken, *LocalUser, error) {
	query := `
		SELECT t.id, t.user_id, t.name, t.token_hash, t.last_used_at, t.expires_at, t.created_at,
		       u.id, u.username, u.email, u.name, u.phone, u.password_hash, u.is_superadmin,
		       u.is_active, u.force_password_change, u.failed_login_attempts,
		       u.locked_until, u.last_login_at, u.created_at, u.updated_at
		FROM api_tokens t
		JOIN local_users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND (t.expires_at IS NULL OR t.expires_at > NOW())
		  AND u.is_active = true
	`
	row := db.pool.QueryRow(ctx, query, tokenHash)

	var tok APIToken
	var storedHash string
	var u LocalUser
	err := row.Scan(
		&tok.ID, &tok.UserID, &tok.Name, &storedHash, &tok.LastUsedAt, &tok.ExpiresAt, &tok.CreatedAt,
		&u.ID, &u.Username, &u.Email, &u.Name, &u.Phone, &u.PasswordHash, &u.IsSuperAdmin,
		&u.IsActive, &u.ForcePasswordChange, &u.FailedLoginAttempts,
		&u.LockedUntil, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get api token with user: %w", err)
	}
	tok.StoredHash = storedHash
	return &tok, &u, nil
}

// ListAPITokensByUser returns all tokens owned by the given user, ordered by creation date.
func (db *DB) ListAPITokensByUser(ctx context.Context, userID uuid.UUID) ([]APIToken, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, name, last_used_at, expires_at, created_at
		FROM api_tokens
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()

	var tokens []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		tokens = append(tokens, *t)
	}
	return tokens, rows.Err()
}

// DeleteAPIToken removes a token by ID, scoped to the owning user so that
// one superadmin cannot revoke another user's tokens.
func (db *DB) DeleteAPIToken(ctx context.Context, id, userID uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `
		DELETE FROM api_tokens WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// TouchAPIToken updates the last_used_at timestamp. Called asynchronously on each
// authenticated request so it does not block the handler.
func (db *DB) TouchAPIToken(ctx context.Context, id uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("touch api token: %w", err)
	}
	return nil
}

func scanAPIToken(row interface {
	Scan(dest ...any) error
}) (*APIToken, error) {
	var t APIToken
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
