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
)

// GetUser retrieves a user by ID
func (db *DB) GetUser(ctx context.Context, userID uuid.UUID) (*User, error) {
	query := `
		SELECT id, team_id, name, email, phone, role, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	user := &User{}
	err := db.pool.QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&user.TeamID,
		&user.Name,
		&user.Email,
		&user.Phone,
		&user.Role,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// CreateUser inserts a new on-call roster member for a team.
func (db *DB) CreateUser(ctx context.Context, u *User) error {
	query := `
		INSERT INTO users (team_id, name, email, phone, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	return db.pool.QueryRow(ctx, query,
		u.TeamID, u.Name, u.Email, u.Phone, u.Role,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
}

// UpdateUser updates a roster member's details, scoped to the team.
func (db *DB) UpdateUser(ctx context.Context, u *User) error {
	query := `
		UPDATE users
		SET name = $1, email = $2, phone = $3, role = $4, updated_at = NOW()
		WHERE id = $5 AND team_id = $6
		RETURNING updated_at
	`
	return db.pool.QueryRow(ctx, query,
		u.Name, u.Email, u.Phone, u.Role, u.ID, u.TeamID,
	).Scan(&u.UpdatedAt)
}

// DeleteUser removes a roster member, scoped to the team.
func (db *DB) DeleteUser(ctx context.Context, teamID, userID uuid.UUID) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM users WHERE id = $1 AND team_id = $2`,
		userID, teamID,
	)
	return err
}

// GetTeamUsers retrieves all users for a team
func (db *DB) GetTeamUsers(ctx context.Context, teamID uuid.UUID) ([]*User, error) {
	query := `
		SELECT id, team_id, name, email, phone, role, created_at, updated_at
		FROM users
		WHERE team_id = $1
		ORDER BY name
	`

	rows, err := db.pool.Query(ctx, query, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	users := []*User{}
	for rows.Next() {
		user := &User{}
		err := rows.Scan(
			&user.ID,
			&user.TeamID,
			&user.Name,
			&user.Email,
			&user.Phone,
			&user.Role,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}

	return users, nil
}
