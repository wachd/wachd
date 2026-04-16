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

// GetTeamConfig retrieves the configuration for a team.
// Returns nil, nil if no config row exists yet.
func (db *DB) GetTeamConfig(ctx context.Context, teamID uuid.UUID) (*TeamConfig, error) {
	query := `
		SELECT
			team_id, slack_webhook_url, slack_channel, slack_bot_token,
			github_token_encrypted, github_repos,
			prometheus_endpoint, loki_endpoint,
			dynatrace_endpoint, dynatrace_token_encrypted,
			splunk_endpoint, splunk_token_encrypted,
			ai_backend, ai_model,
			created_at, updated_at
		FROM team_config
		WHERE team_id = $1
	`

	var tc TeamConfig
	err := db.pool.QueryRow(ctx, query, teamID).Scan(
		&tc.TeamID,
		&tc.SlackWebhookURL,
		&tc.SlackChannel,
		&tc.SlackBotToken,
		&tc.GitHubTokenEncrypted,
		&tc.GitHubRepos,
		&tc.PrometheusEndpoint,
		&tc.LokiEndpoint,
		&tc.DynatraceEndpoint,
		&tc.DynatraceTokenEncrypted,
		&tc.SplunkEndpoint,
		&tc.SplunkTokenEncrypted,
		&tc.AIBackend,
		&tc.AIModel,
		&tc.CreatedAt,
		&tc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &tc, nil
}

// UpsertTeamConfig inserts or updates the configuration for a team.
func (db *DB) UpsertTeamConfig(ctx context.Context, tc *TeamConfig) error {
	query := `
		INSERT INTO team_config (
			team_id, slack_webhook_url, slack_channel, slack_bot_token,
			github_token_encrypted, github_repos,
			prometheus_endpoint, loki_endpoint,
			dynatrace_endpoint, dynatrace_token_encrypted,
			splunk_endpoint, splunk_token_encrypted,
			ai_backend, ai_model,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (team_id) DO UPDATE SET
			slack_webhook_url         = EXCLUDED.slack_webhook_url,
			slack_channel             = EXCLUDED.slack_channel,
			slack_bot_token           = EXCLUDED.slack_bot_token,
			github_token_encrypted    = EXCLUDED.github_token_encrypted,
			github_repos              = EXCLUDED.github_repos,
			prometheus_endpoint       = EXCLUDED.prometheus_endpoint,
			loki_endpoint             = EXCLUDED.loki_endpoint,
			dynatrace_endpoint        = EXCLUDED.dynatrace_endpoint,
			dynatrace_token_encrypted = EXCLUDED.dynatrace_token_encrypted,
			splunk_endpoint           = EXCLUDED.splunk_endpoint,
			splunk_token_encrypted    = EXCLUDED.splunk_token_encrypted,
			ai_backend                = EXCLUDED.ai_backend,
			ai_model                  = EXCLUDED.ai_model,
			updated_at                = EXCLUDED.updated_at
	`

	now := time.Now()
	_, err := db.pool.Exec(ctx, query,
		tc.TeamID,
		tc.SlackWebhookURL,
		tc.SlackChannel,
		tc.SlackBotToken,
		tc.GitHubTokenEncrypted,
		tc.GitHubRepos,
		tc.PrometheusEndpoint,
		tc.LokiEndpoint,
		tc.DynatraceEndpoint,
		tc.DynatraceTokenEncrypted,
		tc.SplunkEndpoint,
		tc.SplunkTokenEncrypted,
		tc.AIBackend,
		tc.AIModel,
		now,
		now,
	)
	return err
}
