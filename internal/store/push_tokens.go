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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SavePushToken upserts a device token for a user.
// If the token already exists it is reassigned to the current user + team
// (handles app reinstalls where the same token reappears under a new session).
func (db *DB) SavePushToken(ctx context.Context, userID uuid.UUID, userSource, token, platform string, teamID uuid.UUID) (*UserPushToken, error) {
	pt := &UserPushToken{}
	err := db.pool.QueryRow(ctx, `
		INSERT INTO user_push_tokens (id, user_id, user_source, token, platform, team_id, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, now())
		ON CONFLICT (token)
		DO UPDATE SET user_id     = EXCLUDED.user_id,
		              user_source = EXCLUDED.user_source,
		              platform    = EXCLUDED.platform,
		              team_id     = EXCLUDED.team_id,
		              created_at  = now()
		RETURNING id, user_id, user_source, token, platform, team_id, created_at
	`, userID, userSource, token, platform, teamID,
	).Scan(&pt.ID, &pt.UserID, &pt.UserSource, &pt.Token, &pt.Platform, &pt.TeamID, &pt.CreatedAt)
	if err != nil {
		return nil, err
	}
	return pt, nil
}

// DeletePushToken removes a specific device token. Only deletes if the token
// belongs to the requesting user to prevent cross-user token deletion.
func (db *DB) DeletePushToken(ctx context.Context, userID uuid.UUID, userSource, token string) error {
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM user_push_tokens
		WHERE token = $1 AND user_id = $2 AND user_source = $3
	`, token, userID, userSource)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetPushTokensByUserID returns all registered device tokens for a user across all platforms.
func (db *DB) GetPushTokensByUserID(ctx context.Context, userID uuid.UUID, userSource string) ([]*UserPushToken, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, user_source, token, platform, team_id, created_at
		FROM user_push_tokens
		WHERE user_id = $1 AND user_source = $2
		ORDER BY created_at DESC
	`, userID, userSource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*UserPushToken
	for rows.Next() {
		pt := &UserPushToken{}
		if err := rows.Scan(&pt.ID, &pt.UserID, &pt.UserSource, &pt.Token, &pt.Platform, &pt.TeamID, &pt.CreatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, pt)
	}
	return tokens, rows.Err()
}
