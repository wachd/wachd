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
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertSSOIdentity creates or updates an SSO identity, returning the current record.
func (db *DB) UpsertSSOIdentity(ctx context.Context, provider, providerID, email, name string, avatarURL *string) (*SSOIdentity, error) {
	query := `
		INSERT INTO sso_identities (provider, provider_id, email, name, avatar_url, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (provider, provider_id)
		DO UPDATE SET
			email      = EXCLUDED.email,
			name       = EXCLUDED.name,
			avatar_url = EXCLUDED.avatar_url,
			updated_at = NOW()
		RETURNING id, provider, provider_id, email, name, avatar_url, created_at, updated_at
	`
	row := db.pool.QueryRow(ctx, query, provider, providerID, email, name, avatarURL)

	var id SSOIdentity
	err := row.Scan(
		&id.ID, &id.Provider, &id.ProviderID,
		&id.Email, &id.Name, &id.AvatarURL,
		&id.CreatedAt, &id.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert sso identity: %w", err)
	}
	return &id, nil
}

// GetGroupMappings returns all group mappings for a provider.
func (db *DB) GetGroupMappings(ctx context.Context, provider string) ([]GroupMapping, error) {
	query := `
		SELECT id, provider, sso_provider_id, group_id, group_name, team_id, role, created_at
		FROM group_mappings
		WHERE provider = $1
		ORDER BY group_name
	`
	rows, err := db.pool.Query(ctx, query, provider)
	if err != nil {
		return nil, fmt.Errorf("get group mappings: %w", err)
	}
	defer rows.Close()

	var mappings []GroupMapping
	for rows.Next() {
		var m GroupMapping
		if err := rows.Scan(&m.ID, &m.Provider, &m.SSOProviderID, &m.GroupID, &m.GroupName, &m.TeamID, &m.Role, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan group mapping: %w", err)
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

// ListGroupMappings returns all group mappings across all providers.
func (db *DB) ListGroupMappings(ctx context.Context) ([]GroupMapping, error) {
	query := `
		SELECT id, provider, sso_provider_id, group_id, group_name, team_id, role, created_at
		FROM group_mappings
		ORDER BY provider, group_name
	`
	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list group mappings: %w", err)
	}
	defer rows.Close()

	var mappings []GroupMapping
	for rows.Next() {
		var m GroupMapping
		if err := rows.Scan(&m.ID, &m.Provider, &m.SSOProviderID, &m.GroupID, &m.GroupName, &m.TeamID, &m.Role, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan group mapping: %w", err)
		}
		mappings = append(mappings, m)
	}
	return mappings, rows.Err()
}

// CreateGroupMapping inserts a new group → team mapping linked to an SSO provider UUID.
func (db *DB) CreateGroupMapping(ctx context.Context, providerID uuid.UUID, groupID string, groupName *string, teamID uuid.UUID, role string) (*GroupMapping, error) {
	groupID = strings.TrimSpace(groupID)
	query := `
		INSERT INTO group_mappings (sso_provider_id, group_id, group_name, team_id, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, provider, sso_provider_id, group_id, group_name, team_id, role, created_at
	`
	row := db.pool.QueryRow(ctx, query, providerID, groupID, groupName, teamID, role)

	var m GroupMapping
	err := row.Scan(&m.ID, &m.Provider, &m.SSOProviderID, &m.GroupID, &m.GroupName, &m.TeamID, &m.Role, &m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create group mapping: %w", err)
	}
	return &m, nil
}

// DeleteGroupMapping removes a group mapping by ID.
func (db *DB) DeleteGroupMapping(ctx context.Context, mappingID uuid.UUID) error {
	query := `DELETE FROM group_mappings WHERE id = $1`
	ct, err := db.pool.Exec(ctx, query, mappingID)
	if err != nil {
		return fmt.Errorf("delete group mapping: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SyncTeamAccess reconciles the user's team access based on current group memberships.
// It grants access to teams whose groups the user belongs to, and revokes access to
// teams whose groups the user no longer belongs to.
// providerID is the UUID of the SSO provider record; used to match group_mappings.
func (db *DB) SyncTeamAccess(ctx context.Context, identityID uuid.UUID, groupIDs []string, providerID uuid.UUID) error {
	if len(groupIDs) == 0 {
		// No groups → revoke all team access for this identity
		_, err := db.pool.Exec(ctx, `DELETE FROM sso_team_access WHERE identity_id = $1`, identityID)
		return err
	}

	// Find which teams these groups map to, matching by provider UUID.
	// Trim any accidental whitespace from the incoming group IDs.
	trimmed := make([]string, len(groupIDs))
	for i, g := range groupIDs {
		trimmed[i] = strings.TrimSpace(g)
	}
	// Match group mappings by either:
	//   (a) explicit sso_provider_id UUID — for providers created via the admin API
	//   (b) provider="entra" with NULL sso_provider_id — for bootstrap mappings from Helm values
	query := `
		SELECT team_id, role FROM group_mappings
		WHERE group_id = ANY($2)
		  AND (
		        (sso_provider_id = $1 AND $1 != '00000000-0000-0000-0000-000000000000'::uuid)
		     OR (sso_provider_id IS NULL AND provider = 'entra')
		      )
	`
	log.Printf("store: SyncTeamAccess providerID=%s groupIDs=%v", providerID, trimmed)
	rows, err := db.pool.Query(ctx, query, providerID, trimmed)
	if err != nil {
		return fmt.Errorf("sync team access query: %w", err)
	}
	defer rows.Close()

	type teamRole struct {
		teamID uuid.UUID
		role   string
	}
	var grants []teamRole
	for rows.Next() {
		var tr teamRole
		if err := rows.Scan(&tr.teamID, &tr.role); err != nil {
			return fmt.Errorf("scan team role: %w", err)
		}
		grants = append(grants, tr)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Printf("store: SyncTeamAccess found %d team grant(s) for identity %s", len(grants), identityID)

	// Upsert access for each mapped team
	for _, g := range grants {
		_, err := db.pool.Exec(ctx, `
			INSERT INTO sso_team_access (identity_id, team_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (identity_id, team_id)
			DO UPDATE SET role = EXCLUDED.role
		`, identityID, g.teamID, g.role)
		if err != nil {
			return fmt.Errorf("upsert team access: %w", err)
		}
	}

	// Build set of team IDs that should remain
	keep := make(map[uuid.UUID]bool, len(grants))
	for _, g := range grants {
		keep[g.teamID] = true
	}

	// Revoke access to teams no longer in the set
	existingRows, err := db.pool.Query(ctx, `
		SELECT team_id FROM sso_team_access WHERE identity_id = $1
	`, identityID)
	if err != nil {
		return fmt.Errorf("get existing access: %w", err)
	}
	defer existingRows.Close()

	var toRevoke []uuid.UUID
	for existingRows.Next() {
		var tid uuid.UUID
		if err := existingRows.Scan(&tid); err != nil {
			return err
		}
		if !keep[tid] {
			toRevoke = append(toRevoke, tid)
		}
	}
	if err := existingRows.Err(); err != nil {
		return err
	}

	for _, tid := range toRevoke {
		_, err := db.pool.Exec(ctx, `
			DELETE FROM sso_team_access WHERE identity_id = $1 AND team_id = $2
		`, identityID, tid)
		if err != nil {
			return fmt.Errorf("revoke team access: %w", err)
		}
	}

	return nil
}

// GetIdentityTeams returns all team accesses for a given SSO identity.
func (db *DB) GetIdentityTeams(ctx context.Context, identityID uuid.UUID) ([]TeamAccess, error) {
	query := `
		SELECT a.identity_id, a.team_id, t.name, a.role
		FROM sso_team_access a
		JOIN teams t ON t.id = a.team_id
		WHERE a.identity_id = $1
		ORDER BY t.name
	`
	rows, err := db.pool.Query(ctx, query, identityID)
	if err != nil {
		return nil, fmt.Errorf("get identity teams: %w", err)
	}
	defer rows.Close()

	var teams []TeamAccess
	for rows.Next() {
		var ta TeamAccess
		if err := rows.Scan(&ta.IdentityID, &ta.TeamID, &ta.TeamName, &ta.Role); err != nil {
			return nil, fmt.Errorf("scan team access: %w", err)
		}
		teams = append(teams, ta)
	}
	return teams, rows.Err()
}

// RecordSession writes a session audit record to the database.
func (db *DB) RecordSession(ctx context.Context, identityID uuid.UUID, tokenHash string, expiresAt time.Time, ipAddress string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO sessions (identity_id, token_hash, expires_at, ip_address)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (token_hash) DO NOTHING
	`, identityID, tokenHash, expiresAt, ipAddress)
	return err
}

// DeleteSessionByHash removes the session audit record when a user logs out.
func (db *DB) DeleteSessionByHash(ctx context.Context, tokenHash string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// EnsureGroupMappingBootstrap creates a group mapping if it doesn't already exist.
// Used on server startup to seed mappings from Helm values.
func (db *DB) EnsureGroupMappingBootstrap(ctx context.Context, provider, groupID string, groupName *string, teamID uuid.UUID, role string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO group_mappings (provider, group_id, group_name, team_id, role)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider, group_id, team_id) DO NOTHING
	`, provider, groupID, groupName, teamID, role)
	return err
}
