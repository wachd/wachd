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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetTeamMembers returns everyone who has access to a team, drawn from both
// local_users (via group membership) and sso_identities (via sso_team_access).
// This is the authoritative list for on-call scheduling and incident assignment.
func (db *DB) GetTeamMembers(ctx context.Context, teamID uuid.UUID) ([]*TeamMember, error) {
	// Most permissive role wins when a user has access via multiple grants.
	query := `
		SELECT id, 'local' AS source, $1::uuid AS team_id, name, email, phone,
		       CASE WHEN bool_or(role = 'admin')     THEN 'admin'
		            WHEN bool_or(role = 'responder') THEN 'responder'
		            ELSE 'viewer' END AS role
		FROM (
			SELECT lu.id, lu.name, lu.email, lu.phone, lga.role
			FROM local_group_members lgm
			JOIN local_group_access lga ON lga.group_id = lgm.group_id AND lga.team_id = $1
			JOIN local_users lu ON lu.id = lgm.user_id
			WHERE lu.is_active = true
		) t
		GROUP BY id, name, email, phone

		UNION

		SELECT si.id, 'sso' AS source, $1::uuid AS team_id, si.name, si.email, si.phone,
		       CASE WHEN bool_or(sta.role = 'admin')     THEN 'admin'
		            WHEN bool_or(sta.role = 'responder') THEN 'responder'
		            ELSE 'viewer' END AS role
		FROM sso_team_access sta
		JOIN sso_identities si ON si.id = sta.identity_id
		WHERE sta.team_id = $1
		GROUP BY si.id, si.name, si.email, si.phone

		ORDER BY name
	`
	rows, err := db.pool.Query(ctx, query, teamID)
	if err != nil {
		return nil, fmt.Errorf("get team members: %w", err)
	}
	defer rows.Close()

	var members []*TeamMember
	for rows.Next() {
		m := &TeamMember{}
		if err := rows.Scan(&m.ID, &m.Source, &m.TeamID, &m.Name, &m.Email, &m.Phone, &m.Role); err != nil {
			return nil, fmt.Errorf("scan team member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// GetMemberByID resolves any identity UUID to a TeamMember.
// Tries local_users first, then sso_identities. Returns nil, nil if not found.
func (db *DB) GetMemberByID(ctx context.Context, id uuid.UUID) (*TeamMember, error) {
	// Try local first.
	var m TeamMember
	err := db.pool.QueryRow(ctx, `
		SELECT id, 'local', name, email, phone
		FROM local_users WHERE id = $1
	`, id).Scan(&m.ID, &m.Source, &m.Name, &m.Email, &m.Phone)
	if err == nil {
		return &m, nil
	}
	if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("get member by id (local): %w", err)
	}

	// Try SSO.
	err = db.pool.QueryRow(ctx, `
		SELECT id, 'sso', name, email, phone
		FROM sso_identities WHERE id = $1
	`, id).Scan(&m.ID, &m.Source, &m.Name, &m.Email, &m.Phone)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get member by id (sso): %w", err)
	}
	return &m, nil
}

// UpdateMemberPhone sets the phone number on the correct auth table.
// source must be "local" or "sso".
func (db *DB) UpdateMemberPhone(ctx context.Context, id uuid.UUID, source string, phone *string) error {
	switch source {
	case "local":
		_, err := db.pool.Exec(ctx,
			`UPDATE local_users SET phone = $1, updated_at = NOW() WHERE id = $2`,
			phone, id,
		)
		return err
	case "sso":
		_, err := db.pool.Exec(ctx,
			`UPDATE sso_identities SET phone = $1, updated_at = NOW() WHERE id = $2`,
			phone, id,
		)
		return err
	default:
		return fmt.Errorf("unknown member source %q", source)
	}
}
