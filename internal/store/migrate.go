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
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var schema string

// migrateAdvisoryLockID is the pg_advisory_xact_lock key used to serialize
// concurrent Migrate calls (e.g. multiple test processes hitting the same DB).
const migrateAdvisoryLockID = 7428374293

// Migrate runs the embedded schema SQL against the database.
// All statements use IF NOT EXISTS so it is safe to call on every startup.
// A transaction-scoped advisory lock prevents concurrent callers from
// deadlocking when multiple processes (e.g. test suites) start simultaneously.
func (db *DB) Migrate(ctx context.Context) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migration begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialize concurrent migrations. pg_advisory_xact_lock blocks until the
	// lock is available and releases it automatically on commit/rollback.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateAdvisoryLockID); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}

	if _, err := tx.Exec(ctx, schema); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	return tx.Commit(ctx)
}

// CountTeams returns the number of teams in the database.
func (db *DB) CountTeams(ctx context.Context) (int, error) {
	var count int
	if err := db.pool.QueryRow(ctx, "SELECT COUNT(*) FROM teams").Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count teams: %w", err)
	}
	return count, nil
}

// CreateTeam inserts a new team and returns it.
func (db *DB) CreateTeam(ctx context.Context, name, webhookSecret string) (*Team, error) {
	query := `
		INSERT INTO teams (name, webhook_secret)
		VALUES ($1, $2)
		RETURNING id, name, webhook_secret, created_at, updated_at
	`
	team := &Team{}
	err := db.pool.QueryRow(ctx, query, name, webhookSecret).Scan(
		&team.ID,
		&team.Name,
		&team.WebhookSecret,
		&team.CreatedAt,
		&team.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	return team, nil
}
