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

// CountLocalUsers returns the total number of local users. Used to detect first run.
func (db *DB) CountLocalUsers(ctx context.Context) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `SELECT COUNT(*) FROM local_users`).Scan(&n)
	return n, err
}

// CreateLocalUser inserts a new local user and returns the created record.
func (db *DB) CreateLocalUser(ctx context.Context, username, email, name, passwordHash string, isSuperAdmin, forcePasswordChange bool) (*LocalUser, error) {
	query := `
		INSERT INTO local_users
			(username, email, name, password_hash, is_superadmin, force_password_change)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, username, email, name, phone, password_hash, is_superadmin, is_active,
		          force_password_change, failed_login_attempts, locked_until,
		          last_login_at, created_at, updated_at
	`
	row := db.pool.QueryRow(ctx, query, username, email, name, passwordHash, isSuperAdmin, forcePasswordChange)
	return scanLocalUser(row)
}

// GetLocalUserByUsername returns a local user by username, or nil if not found.
func (db *DB) GetLocalUserByUsername(ctx context.Context, username string) (*LocalUser, error) {
	query := `
		SELECT id, username, email, name, phone, password_hash, is_superadmin, is_active,
		       force_password_change, failed_login_attempts, locked_until,
		       last_login_at, created_at, updated_at
		FROM local_users WHERE username = $1
	`
	row := db.pool.QueryRow(ctx, query, username)
	u, err := scanLocalUser(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// GetLocalUserByID returns a local user by ID, or nil if not found.
func (db *DB) GetLocalUserByID(ctx context.Context, id uuid.UUID) (*LocalUser, error) {
	query := `
		SELECT id, username, email, name, phone, password_hash, is_superadmin, is_active,
		       force_password_change, failed_login_attempts, locked_until,
		       last_login_at, created_at, updated_at
		FROM local_users WHERE id = $1
	`
	row := db.pool.QueryRow(ctx, query, id)
	u, err := scanLocalUser(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// ListLocalUsers returns all local users ordered by username.
func (db *DB) ListLocalUsers(ctx context.Context) ([]LocalUser, error) {
	query := `
		SELECT id, username, email, name, phone, password_hash, is_superadmin, is_active,
		       force_password_change, failed_login_attempts, locked_until,
		       last_login_at, created_at, updated_at
		FROM local_users ORDER BY username
	`
	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list local users: %w", err)
	}
	defer rows.Close()

	var users []LocalUser
	for rows.Next() {
		u, err := scanLocalUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan local user: %w", err)
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// UpdateLocalUser applies partial updates (email, name, is_active).
func (db *DB) UpdateLocalUser(ctx context.Context, id uuid.UUID, u LocalUserUpdate) (*LocalUser, error) {
	args := []any{id}
	set := ""
	add := func(col string, val any) {
		if set != "" {
			set += ", "
		}
		args = append(args, val)
		set += fmt.Sprintf("%s = $%d", col, len(args))
	}

	if u.Email != nil {
		add("email", *u.Email)
	}
	if u.Name != nil {
		add("name", *u.Name)
	}
	if u.IsActive != nil {
		add("is_active", *u.IsActive)
	}

	if set == "" {
		return db.GetLocalUserByID(ctx, id)
	}
	set += ", updated_at = NOW()"

	query := fmt.Sprintf(`
		UPDATE local_users SET %s WHERE id = $1
		RETURNING id, username, email, name, phone, password_hash, is_superadmin, is_active,
		          force_password_change, failed_login_attempts, locked_until,
		          last_login_at, created_at, updated_at
	`, set)

	row := db.pool.QueryRow(ctx, query, args...)
	return scanLocalUser(row)
}

// DeleteLocalUser removes a local user by ID.
func (db *DB) DeleteLocalUser(ctx context.Context, id uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `DELETE FROM local_users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete local user: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// UpdatePasswordHash sets a new bcrypt hash for a local user and optionally
// clears the force_password_change flag.
func (db *DB) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string, forceChange bool) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE local_users
		SET password_hash = $2, force_password_change = $3,
		    failed_login_attempts = 0, locked_until = NULL, updated_at = NOW()
		WHERE id = $1
	`, id, hash, forceChange)
	return err
}

// IncrementFailedAttempts increments the failed login counter.
// When lockUntil is non-nil, also sets locked_until.
func (db *DB) IncrementFailedAttempts(ctx context.Context, id uuid.UUID, lockUntil *time.Time) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE local_users
		SET failed_login_attempts = failed_login_attempts + 1,
		    locked_until = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, id, lockUntil)
	return err
}

// ResetFailedAttempts clears the failed login counter and lock.
func (db *DB) ResetFailedAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE local_users
		SET failed_login_attempts = 0, locked_until = NULL, updated_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// RecordLocalLogin updates the last_login_at timestamp.
func (db *DB) RecordLocalLogin(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, `
		UPDATE local_users SET last_login_at = $2, updated_at = NOW() WHERE id = $1
	`, id, now)
	return err
}

// scanLocalUser reads a LocalUser from any pgx row/rows scanner.
func scanLocalUser(row interface {
	Scan(dest ...any) error
}) (*LocalUser, error) {
	var u LocalUser
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.Name, &u.Phone, &u.PasswordHash,
		&u.IsSuperAdmin, &u.IsActive, &u.ForcePasswordChange,
		&u.FailedLoginAttempts, &u.LockedUntil,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
