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
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetSystemConfig returns the platform-wide system configuration.
// Returns a default config (ollama backend) if no row exists yet.
func (db *DB) GetSystemConfig(ctx context.Context) (*SystemConfig, error) {
	query := `
		SELECT ai_backend, ai_model, updated_at, updated_by
		FROM system_config
		WHERE id = 1
	`
	var sc SystemConfig
	err := db.pool.QueryRow(ctx, query).Scan(
		&sc.AIBackend,
		&sc.AIModel,
		&sc.UpdatedAt,
		&sc.UpdatedBy,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &SystemConfig{AIBackend: "ollama", UpdatedAt: time.Now().UTC()}, nil
		}
		return nil, err
	}
	return &sc, nil
}

// UpsertSystemConfig inserts or updates the platform-wide system configuration.
// updatedBy is the local user ID making the change (for audit trail).
func (db *DB) UpsertSystemConfig(ctx context.Context, backend string, model *string, updatedBy uuid.UUID) (*SystemConfig, error) {
	query := `
		INSERT INTO system_config (id, ai_backend, ai_model, updated_at, updated_by)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			ai_backend = EXCLUDED.ai_backend,
			ai_model   = EXCLUDED.ai_model,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by
		RETURNING ai_backend, ai_model, updated_at, updated_by
	`
	now := time.Now().UTC()
	var sc SystemConfig
	err := db.pool.QueryRow(ctx, query, backend, model, now, updatedBy).Scan(
		&sc.AIBackend,
		&sc.AIModel,
		&sc.UpdatedAt,
		&sc.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

// SeedSystemConfig writes the initial AI backend config from environment variables
// if no system_config row exists yet. Called once at server and worker startup.
func (db *DB) SeedSystemConfig(ctx context.Context, backend string, model *string) error {
	query := `
		INSERT INTO system_config (id, ai_backend, ai_model, updated_at)
		VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`
	_, err := db.pool.Exec(ctx, query, backend, model, time.Now().UTC())
	return err
}
