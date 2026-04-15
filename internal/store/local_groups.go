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

// CreateLocalGroup inserts a new local group.
func (db *DB) CreateLocalGroup(ctx context.Context, name, description string) (*LocalGroup, error) {
	query := `
		INSERT INTO local_groups (name, description)
		VALUES ($1, $2)
		RETURNING id, name, description, created_at, updated_at
	`
	row := db.pool.QueryRow(ctx, query, name, description)
	return scanLocalGroup(row)
}

// ListLocalGroups returns all local groups ordered by name.
func (db *DB) ListLocalGroups(ctx context.Context) ([]LocalGroup, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, name, description, created_at, updated_at
		FROM local_groups ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list local groups: %w", err)
	}
	defer rows.Close()

	var groups []LocalGroup
	for rows.Next() {
		g, err := scanLocalGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scan local group: %w", err)
		}
		groups = append(groups, *g)
	}
	return groups, rows.Err()
}

// DeleteLocalGroup removes a group by ID.
func (db *DB) DeleteLocalGroup(ctx context.Context, id uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `DELETE FROM local_groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete local group: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// AddGroupMember adds a local user to a local group.
func (db *DB) AddGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO local_group_members (group_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, groupID, userID)
	return err
}

// RemoveGroupMember removes a local user from a local group.
func (db *DB) RemoveGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `
		DELETE FROM local_group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, userID)
	if err != nil {
		return fmt.Errorf("remove group member: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListGroupMembers returns all users in a group.
func (db *DB) ListGroupMembers(ctx context.Context, groupID uuid.UUID) ([]LocalUser, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT u.id, u.username, u.email, u.name, u.phone, u.password_hash, u.is_superadmin, u.is_active,
		       u.force_password_change, u.failed_login_attempts, u.locked_until,
		       u.last_login_at, u.created_at, u.updated_at
		FROM local_users u
		JOIN local_group_members m ON m.user_id = u.id
		WHERE m.group_id = $1
		ORDER BY u.username
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()

	var users []LocalUser
	for rows.Next() {
		u, err := scanLocalUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// GrantGroupAccess grants a local group access to a team with a given role.
func (db *DB) GrantGroupAccess(ctx context.Context, groupID, teamID uuid.UUID, role string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO local_group_access (group_id, team_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id, team_id) DO UPDATE SET role = EXCLUDED.role
	`, groupID, teamID, role)
	return err
}

// RevokeGroupAccess removes a group's access to a team.
func (db *DB) RevokeGroupAccess(ctx context.Context, groupID, teamID uuid.UUID) error {
	ct, err := db.pool.Exec(ctx, `
		DELETE FROM local_group_access WHERE group_id = $1 AND team_id = $2
	`, groupID, teamID)
	if err != nil {
		return fmt.Errorf("revoke group access: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListGroupAccess returns all team access entries for a group.
func (db *DB) ListGroupAccess(ctx context.Context, groupID uuid.UUID) ([]TeamAccess, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT lga.group_id, lga.team_id, t.name, lga.role
		FROM local_group_access lga
		JOIN teams t ON t.id = lga.team_id
		WHERE lga.group_id = $1
		ORDER BY t.name
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group access: %w", err)
	}
	defer rows.Close()

	var access []TeamAccess
	for rows.Next() {
		var ta TeamAccess
		if err := rows.Scan(&ta.IdentityID, &ta.TeamID, &ta.TeamName, &ta.Role); err != nil {
			return nil, fmt.Errorf("scan group access: %w", err)
		}
		access = append(access, ta)
	}
	return access, rows.Err()
}

// GetLocalUserTeams returns all team accesses for a local user,
// resolved via group membership → group access → team.
func (db *DB) GetLocalUserTeams(ctx context.Context, userID uuid.UUID) ([]TeamAccess, error) {
	query := `
		SELECT lga.team_id, t.name, lga.role
		FROM local_group_members lgm
		JOIN local_group_access lga ON lga.group_id = lgm.group_id
		JOIN teams t ON t.id = lga.team_id
		WHERE lgm.user_id = $1
		ORDER BY t.name
	`
	rows, err := db.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get local user teams: %w", err)
	}
	defer rows.Close()

	var teams []TeamAccess
	for rows.Next() {
		var ta TeamAccess
		if err := rows.Scan(&ta.TeamID, &ta.TeamName, &ta.Role); err != nil {
			return nil, fmt.Errorf("scan team access: %w", err)
		}
		ta.IdentityID = userID // re-use field to hold user ID
		teams = append(teams, ta)
	}
	return teams, rows.Err()
}

func scanLocalGroup(row interface {
	Scan(dest ...any) error
}) (*LocalGroup, error) {
	var g LocalGroup
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}
