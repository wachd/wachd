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
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetTeamByWebhookSecret retrieves a team by its webhook secret
func (db *DB) GetTeamByWebhookSecret(ctx context.Context, secret string) (*Team, error) {
	query := `
		SELECT id, name, webhook_secret, created_at, updated_at
		FROM teams
		WHERE webhook_secret = $1
	`

	team := &Team{}
	err := db.pool.QueryRow(ctx, query, secret).Scan(
		&team.ID,
		&team.Name,
		&team.WebhookSecret,
		&team.CreatedAt,
		&team.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get team by webhook secret: %w", err)
	}

	return team, nil
}

// GetOrCreateTeamByName returns an existing team with the given name, or creates one.
// Used during group mapping bootstrap.
func (db *DB) GetOrCreateTeamByName(ctx context.Context, name string) (*Team, error) {
	// Try to find existing team by name
	team := &Team{}
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, webhook_secret, created_at, updated_at
		FROM teams WHERE name = $1 LIMIT 1
	`, name).Scan(&team.ID, &team.Name, &team.WebhookSecret, &team.CreatedAt, &team.UpdatedAt)
	if err == nil {
		return team, nil
	}

	// Create new team with a random webhook secret
	secret, err := randomWebhookSecret()
	if err != nil {
		return nil, fmt.Errorf("generate webhook secret: %w", err)
	}

	err = db.pool.QueryRow(ctx, `
		INSERT INTO teams (name, webhook_secret)
		VALUES ($1, $2)
		RETURNING id, name, webhook_secret, created_at, updated_at
	`, name, secret).Scan(&team.ID, &team.Name, &team.WebhookSecret, &team.CreatedAt, &team.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create team %q: %w", name, err)
	}
	return team, nil
}

func randomWebhookSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (db *DB) GetTeam(ctx context.Context, teamID uuid.UUID) (*Team, error) {
	query := `
		SELECT id, name, webhook_secret, created_at, updated_at
		FROM teams
		WHERE id = $1
	`

	team := &Team{}
	err := db.pool.QueryRow(ctx, query, teamID).Scan(
		&team.ID,
		&team.Name,
		&team.WebhookSecret,
		&team.CreatedAt,
		&team.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get team: %w", err)
	}

	return team, nil
}

// ListTeams returns all teams ordered by name.
func (db *DB) ListTeams(ctx context.Context) ([]Team, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, name, webhook_secret, created_at, updated_at
		FROM teams ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.WebhookSecret, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// DeleteTeam removes a team and all its cascaded data (incidents, schedules, overrides, etc).
func (db *DB) DeleteTeam(ctx context.Context, teamID uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
