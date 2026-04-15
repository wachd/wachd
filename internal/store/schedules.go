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
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ScheduleResponse represents the schedule with parsed rotation config
type ScheduleResponse struct {
	ID             uuid.UUID              `json:"id"`
	TeamID         uuid.UUID              `json:"team_id"`
	Name           string                 `json:"name"`
	RotationConfig map[string]interface{} `json:"rotation_config"`
	Enabled        bool                   `json:"enabled"`
	CreatedAt      string                 `json:"created_at"`
	UpdatedAt      string                 `json:"updated_at"`
}

// UpsertSchedule creates or updates the team's on-call schedule.
// Updates in-place if a schedule already exists for the team; inserts otherwise.
func (db *DB) UpsertSchedule(ctx context.Context, s *Schedule) error {
	// Try UPDATE first (covers teams that already have a schedule).
	updateErr := db.pool.QueryRow(ctx, `
		UPDATE schedules
		SET name = $1, rotation_config = $2, enabled = $3, updated_at = NOW()
		WHERE team_id = $4
		RETURNING id, created_at, updated_at
	`, s.Name, s.RotationConfig, s.Enabled, s.TeamID).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)

	if updateErr == nil {
		return nil // updated existing row
	}
	if updateErr != pgx.ErrNoRows {
		return updateErr
	}

	// No existing row — INSERT.
	return db.pool.QueryRow(ctx, `
		INSERT INTO schedules (team_id, name, rotation_config, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at
	`, s.TeamID, s.Name, s.RotationConfig, s.Enabled).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}

// GetSchedule retrieves the active on-call schedule for a team.
func (db *DB) GetSchedule(ctx context.Context, teamID uuid.UUID) (*Schedule, error) {
	query := `
		SELECT id, team_id, name, rotation_config, enabled, created_at, updated_at
		FROM schedules
		WHERE team_id = $1 AND enabled = true
		LIMIT 1
	`
	var s Schedule
	err := db.pool.QueryRow(ctx, query, teamID).Scan(
		&s.ID, &s.TeamID, &s.Name, &s.RotationConfig,
		&s.Enabled, &s.CreatedAt, &s.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil // no schedule configured yet — not an error
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetScheduleByID retrieves a schedule by its primary key, scoped to a team.
func (db *DB) GetScheduleByID(ctx context.Context, id, teamID uuid.UUID) (*Schedule, error) {
	query := `
		SELECT id, team_id, name, rotation_config, enabled, created_at, updated_at
		FROM schedules WHERE id = $1 AND team_id = $2
	`
	var s Schedule
	err := db.pool.QueryRow(ctx, query, id, teamID).Scan(
		&s.ID, &s.TeamID, &s.Name, &s.RotationConfig,
		&s.Enabled, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetScheduleForAPI retrieves the schedule with rotation config as a map for the API.
// Returns nil, nil if no schedule is configured for the team.
func (db *DB) GetScheduleForAPI(ctx context.Context, teamID uuid.UUID) (*ScheduleResponse, error) {
	schedule, err := db.GetSchedule(ctx, teamID)
	if err != nil {
		return nil, err
	}
	if schedule == nil {
		return nil, nil // no schedule configured yet
	}

	var rotationData map[string]interface{}
	if err := json.Unmarshal(schedule.RotationConfig, &rotationData); err != nil {
		return nil, err
	}

	// Transform rotation array to day-indexed map for legacy weekly format.
	rotationMap := make(map[string]interface{})
	if rotation, ok := rotationData["rotation"].([]interface{}); ok {
		for _, item := range rotation {
			if entry, ok := item.(map[string]interface{}); ok {
				if day, ok := entry["day"].(string); ok {
					rotationMap[day] = entry["user_id"]
				}
			}
		}
	}
	// For layered format, pass through as-is.
	if len(rotationMap) == 0 {
		rotationMap = rotationData
	}

	return &ScheduleResponse{
		ID:             schedule.ID,
		TeamID:         schedule.TeamID,
		Name:           schedule.Name,
		RotationConfig: rotationMap,
		Enabled:        schedule.Enabled,
		CreatedAt:      schedule.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      schedule.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

// GetActiveOverrideForSchedule returns the active override for a schedule at time t,
// scoped to the team for defence in depth. Returns nil if none exists.
func (db *DB) GetActiveOverrideForSchedule(ctx context.Context, scheduleID, teamID uuid.UUID, t time.Time) (*ScheduleOverride, error) {
	query := `
		SELECT id, schedule_id, team_id, start_at, end_at, user_id, reason, created_by, created_at
		FROM schedule_overrides
		WHERE schedule_id = $1 AND team_id = $2 AND start_at <= $3 AND end_at > $3
		ORDER BY created_at DESC
		LIMIT 1
	`
	var o ScheduleOverride
	err := db.pool.QueryRow(ctx, query, scheduleID, teamID, t).Scan(
		&o.ID, &o.ScheduleID, &o.TeamID, &o.StartAt, &o.EndAt,
		&o.UserID, &o.Reason, &o.CreatedBy, &o.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOverridesForSchedule returns all upcoming overrides for a schedule (next 30 days),
// scoped to the team.
func (db *DB) ListOverridesForSchedule(ctx context.Context, scheduleID, teamID uuid.UUID) ([]ScheduleOverride, error) {
	query := `
		SELECT id, schedule_id, team_id, start_at, end_at, user_id, reason, created_by, created_at
		FROM schedule_overrides
		WHERE schedule_id = $1 AND team_id = $2 AND end_at > NOW()
		ORDER BY start_at ASC
		LIMIT 100
	`
	rows, err := db.pool.Query(ctx, query, scheduleID, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var overrides []ScheduleOverride
	for rows.Next() {
		var o ScheduleOverride
		if err := rows.Scan(
			&o.ID, &o.ScheduleID, &o.TeamID, &o.StartAt, &o.EndAt,
			&o.UserID, &o.Reason, &o.CreatedBy, &o.CreatedAt,
		); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

// CreateOverride inserts a new schedule override.
func (db *DB) CreateOverride(ctx context.Context, o *ScheduleOverride) error {
	query := `
		INSERT INTO schedule_overrides
			(schedule_id, team_id, start_at, end_at, user_id, reason, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`
	return db.pool.QueryRow(ctx, query,
		o.ScheduleID, o.TeamID, o.StartAt, o.EndAt,
		o.UserID, o.Reason, o.CreatedBy,
	).Scan(&o.ID, &o.CreatedAt)
}

// ListOverridesForRange returns overrides that overlap with [from, to).
func (db *DB) ListOverridesForRange(ctx context.Context, scheduleID, teamID uuid.UUID, from, to time.Time) ([]ScheduleOverride, error) {
	query := `
		SELECT id, schedule_id, team_id, start_at, end_at, user_id, reason, created_by, created_at
		FROM schedule_overrides
		WHERE schedule_id = $1 AND team_id = $2 AND start_at < $4 AND end_at > $3
		ORDER BY start_at ASC
	`
	rows, err := db.pool.Query(ctx, query, scheduleID, teamID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var overrides []ScheduleOverride
	for rows.Next() {
		var o ScheduleOverride
		if err := rows.Scan(
			&o.ID, &o.ScheduleID, &o.TeamID, &o.StartAt, &o.EndAt,
			&o.UserID, &o.Reason, &o.CreatedBy, &o.CreatedAt,
		); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

// DeleteOverride removes an override by ID, scoped to the team for authorization.
func (db *DB) DeleteOverride(ctx context.Context, overrideID, teamID uuid.UUID) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM schedule_overrides WHERE id = $1 AND team_id = $2`,
		overrideID, teamID,
	)
	return err
}

// GetEscalationPolicy returns the escalation policy for a team, or nil if none.
func (db *DB) GetEscalationPolicy(ctx context.Context, teamID uuid.UUID) (*EscalationPolicy, error) {
	query := `SELECT id, team_id, config, updated_at FROM escalation_policies WHERE team_id = $1`
	var p EscalationPolicy
	err := db.pool.QueryRow(ctx, query, teamID).Scan(&p.ID, &p.TeamID, &p.Config, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertEscalationPolicy creates or replaces the escalation policy for a team.
func (db *DB) UpsertEscalationPolicy(ctx context.Context, p *EscalationPolicy) error {
	query := `
		INSERT INTO escalation_policies (team_id, config, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (team_id) DO UPDATE
		SET config = EXCLUDED.config, updated_at = NOW()
		RETURNING id, updated_at
	`
	return db.pool.QueryRow(ctx, query, p.TeamID, p.Config).Scan(&p.ID, &p.UpdatedAt)
}
