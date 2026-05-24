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
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListServiceDependencies returns all declared dependencies for a team.
// If service is non-empty, only dependencies for that service are returned.
func (db *DB) ListServiceDependencies(ctx context.Context, teamID uuid.UUID, service string) ([]*ServiceDependency, error) {
	var rows pgx.Rows
	var err error
	if service != "" {
		rows, err = db.pool.Query(ctx, `
			SELECT id, team_id, service, depends_on, label, created_at
			FROM service_dependencies
			WHERE team_id = $1 AND service = $2
			ORDER BY created_at ASC
		`, teamID, service)
	} else {
		rows, err = db.pool.Query(ctx, `
			SELECT id, team_id, service, depends_on, label, created_at
			FROM service_dependencies
			WHERE team_id = $1
			ORDER BY service ASC, created_at ASC
		`, teamID)
	}
	if err != nil {
		return nil, fmt.Errorf("list service dependencies: %w", err)
	}
	defer rows.Close()

	var deps []*ServiceDependency
	for rows.Next() {
		d := &ServiceDependency{}
		if err := rows.Scan(&d.ID, &d.TeamID, &d.Service, &d.DependsOn, &d.Label, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan service dependency: %w", err)
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// CreateServiceDependency inserts a new service dependency.
// Returns the created record. Returns an error if the (team_id, service, depends_on)
// combination already exists.
func (db *DB) CreateServiceDependency(ctx context.Context, d *ServiceDependency) (*ServiceDependency, error) {
	if d == nil {
		return nil, fmt.Errorf("service dependency is required")
	}
	if d.TeamID == uuid.Nil {
		return nil, fmt.Errorf("team_id is required")
	}
	if strings.TrimSpace(d.Service) == "" {
		return nil, fmt.Errorf("service is required")
	}
	if strings.TrimSpace(d.DependsOn) == "" {
		return nil, fmt.Errorf("depends_on is required")
	}
	if strings.EqualFold(strings.TrimSpace(d.Service), strings.TrimSpace(d.DependsOn)) {
		return nil, fmt.Errorf("service cannot depend on itself")
	}

	created := &ServiceDependency{}
	err := db.pool.QueryRow(ctx, `
		INSERT INTO service_dependencies (team_id, service, depends_on, label)
		VALUES ($1, $2, $3, $4)
		RETURNING id, team_id, service, depends_on, label, created_at
	`, d.TeamID, strings.TrimSpace(d.Service), strings.TrimSpace(d.DependsOn), d.Label).
		Scan(&created.ID, &created.TeamID, &created.Service, &created.DependsOn, &created.Label, &created.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create service dependency: %w", err)
	}
	return created, nil
}

// DeleteServiceDependency removes a service dependency by ID.
// Returns an error if the record does not exist or belongs to a different team.
func (db *DB) DeleteServiceDependency(ctx context.Context, teamID uuid.UUID, id uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM service_dependencies
		WHERE id = $1 AND team_id = $2
	`, id, teamID)
	if err != nil {
		return fmt.Errorf("delete service dependency: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("service dependency not found")
	}
	return nil
}
