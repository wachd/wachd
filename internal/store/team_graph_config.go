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

const defaultTeamGraphMinSimilarityScore = 0.12

// GetTeamGraphConfig returns the graph configuration for a team.
// If no explicit row exists yet, defaults are returned.
func (db *DB) GetTeamGraphConfig(ctx context.Context, teamID uuid.UUID) (*TeamGraphConfig, error) {
	var cfg TeamGraphConfig
	err := db.pool.QueryRow(ctx, `
		SELECT team_id, enabled, min_similarity_score, updated_at
		FROM team_graph_config
		WHERE team_id = $1
	`, teamID).Scan(&cfg.TeamID, &cfg.Enabled, &cfg.MinSimilarityScore, &cfg.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &TeamGraphConfig{
				TeamID:             teamID,
				Enabled:            true,
				MinSimilarityScore: defaultTeamGraphMinSimilarityScore,
			}, nil
		}
		return nil, fmt.Errorf("get team graph config: %w", err)
	}
	return &cfg, nil
}

// UpsertTeamGraphConfig creates or updates the team graph configuration.
func (db *DB) UpsertTeamGraphConfig(ctx context.Context, cfg *TeamGraphConfig) error {
	if cfg == nil {
		return fmt.Errorf("team graph config is required")
	}
	if cfg.TeamID == uuid.Nil {
		return fmt.Errorf("team id is required")
	}
	if cfg.MinSimilarityScore < 0 || cfg.MinSimilarityScore > 1 {
		return fmt.Errorf("min similarity score must be between 0 and 1")
	}
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}

	_, err := db.pool.Exec(ctx, `
		INSERT INTO team_graph_config (team_id, enabled, min_similarity_score, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id)
		DO UPDATE SET
			enabled = EXCLUDED.enabled,
			min_similarity_score = EXCLUDED.min_similarity_score,
			updated_at = EXCLUDED.updated_at
	`, cfg.TeamID, cfg.Enabled, cfg.MinSimilarityScore, cfg.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert team graph config: %w", err)
	}
	return nil
}
