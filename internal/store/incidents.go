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
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateIncident creates a new incident in the database
func (db *DB) CreateIncident(ctx context.Context, incident *Incident) error {
	query := `
		INSERT INTO incidents (
			id, team_id, title, message, severity, status, source,
			alert_payload, fired_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
	`

	now := time.Now().UTC()
	incident.ID = uuid.New()
	incident.CreatedAt = now
	incident.UpdatedAt = now
	incident.FiredAt = now

	_, err := db.pool.Exec(ctx, query,
		incident.ID,
		incident.TeamID,
		incident.Title,
		incident.Message,
		incident.Severity,
		incident.Status,
		incident.Source,
		incident.AlertPayload,
		incident.FiredAt,
		incident.CreatedAt,
		incident.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create incident: %w", err)
	}

	return nil
}

// GetIncident retrieves an incident by ID
func (db *DB) GetIncident(ctx context.Context, teamID, incidentID uuid.UUID) (*Incident, error) {
	query := `
		SELECT
			id, team_id, title, message, severity, status, source,
			alert_payload, context, analysis,
			fired_at, acknowledged_at, resolved_at, snoozed_until,
			escalation_step, created_at, updated_at, assigned_to
		FROM incidents
		WHERE id = $1 AND team_id = $2
	`

	incident := &Incident{}
	err := db.pool.QueryRow(ctx, query, incidentID, teamID).Scan(
		&incident.ID,
		&incident.TeamID,
		&incident.Title,
		&incident.Message,
		&incident.Severity,
		&incident.Status,
		&incident.Source,
		&incident.AlertPayload,
		&incident.Context,
		&incident.Analysis,
		&incident.FiredAt,
		&incident.AcknowledgedAt,
		&incident.ResolvedAt,
		&incident.SnoozedUntil,
		&incident.EscalationStep,
		&incident.CreatedAt,
		&incident.UpdatedAt,
		&incident.AssignedTo,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}

	return incident, nil
}

// ListIncidents retrieves incidents for a team
func (db *DB) ListIncidents(ctx context.Context, teamID uuid.UUID, limit, offset int) ([]*Incident, error) {
	query := `
		SELECT
			id, team_id, title, message, severity, status, source,
			alert_payload, context, analysis,
			fired_at, acknowledged_at, resolved_at, snoozed_until,
			escalation_step, created_at, updated_at, assigned_to
		FROM incidents
		WHERE team_id = $1
		ORDER BY fired_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := db.pool.Query(ctx, query, teamID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list incidents: %w", err)
	}
	defer rows.Close()

	incidents := []*Incident{}
	for rows.Next() {
		incident := &Incident{}
		err := rows.Scan(
			&incident.ID,
			&incident.TeamID,
			&incident.Title,
			&incident.Message,
			&incident.Severity,
			&incident.Status,
			&incident.Source,
			&incident.AlertPayload,
			&incident.Context,
			&incident.Analysis,
			&incident.FiredAt,
			&incident.AcknowledgedAt,
			&incident.ResolvedAt,
			&incident.SnoozedUntil,
			&incident.EscalationStep,
			&incident.CreatedAt,
			&incident.UpdatedAt,
			&incident.AssignedTo,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan incident: %w", err)
		}
		incidents = append(incidents, incident)
	}

	return incidents, nil
}

// UpdateIncidentStatus updates the status of an incident scoped to a team.
func (db *DB) UpdateIncidentStatus(ctx context.Context, teamID, incidentID uuid.UUID, status string) error {
	query := `
		UPDATE incidents
		SET status = $1, updated_at = $2
		WHERE id = $3 AND team_id = $4
	`
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, query, status, now, incidentID, teamID)
	if err != nil {
		return fmt.Errorf("failed to update incident status: %w", err)
	}
	return nil
}

// AcknowledgeIncident marks an incident as acknowledged, scoped to a team.
func (db *DB) AcknowledgeIncident(ctx context.Context, teamID, incidentID uuid.UUID, userID uuid.UUID) error {
	query := `
		UPDATE incidents
		SET
			status = 'acknowledged',
			acknowledged_at = $1,
			assigned_to = $2,
			updated_at = $1
		WHERE id = $3 AND team_id = $4
	`
	now := time.Now().UTC()
	_, err := db.pool.Exec(ctx, query, now, userID, incidentID, teamID)
	if err != nil {
		return fmt.Errorf("failed to acknowledge incident: %w", err)
	}
	return nil
}

// IncidentResponse represents an incident with parsed JSON fields for API
type IncidentResponse struct {
	ID             uuid.UUID              `json:"id"`
	TeamID         uuid.UUID              `json:"team_id"`
	Title          string                 `json:"title"`
	Message        *string                `json:"message,omitempty"`
	Severity       string                 `json:"severity"`
	Status         string                 `json:"status"`
	Source         string                 `json:"source"`
	AlertPayload   map[string]interface{} `json:"alert_payload,omitempty"`
	Context        map[string]interface{} `json:"context,omitempty"`
	Analysis       map[string]interface{} `json:"analysis,omitempty"`
	FiredAt        time.Time              `json:"fired_at"`
	AcknowledgedAt *time.Time             `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time             `json:"resolved_at,omitempty"`
	SnoozedUntil   *time.Time             `json:"snoozed_until,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	AssignedTo     *uuid.UUID             `json:"assigned_to,omitempty"`
}

// ToResponse converts an Incident to IncidentResponse with parsed JSON fields
func (i *Incident) ToResponse() (*IncidentResponse, error) {
	resp := &IncidentResponse{
		ID:             i.ID,
		TeamID:         i.TeamID,
		Title:          i.Title,
		Message:        i.Message,
		Severity:       i.Severity,
		Status:         i.Status,
		Source:         i.Source,
		FiredAt:        i.FiredAt,
		AcknowledgedAt: i.AcknowledgedAt,
		ResolvedAt:     i.ResolvedAt,
		SnoozedUntil:   i.SnoozedUntil,
		CreatedAt:      i.CreatedAt,
		UpdatedAt:      i.UpdatedAt,
		AssignedTo:     i.AssignedTo,
	}

	// Parse alert payload
	if i.AlertPayload != nil {
		var payload map[string]interface{}
		if err := json.Unmarshal(i.AlertPayload, &payload); err == nil {
			resp.AlertPayload = payload
		}
	}

	// Parse context
	if i.Context != nil {
		var context map[string]interface{}
		if err := json.Unmarshal(i.Context, &context); err == nil {
			resp.Context = context
		}
	}

	// Parse analysis
	if i.Analysis != nil {
		var analysis map[string]interface{}
		if err := json.Unmarshal(i.Analysis, &analysis); err == nil {
			resp.Analysis = analysis
		}
	}

	return resp, nil
}

// GetOpenIncidentsForEscalation returns open incidents that fired at least minAge ago.
// Used by the worker's escalation loop to find candidates for the next escalation step.
func (db *DB) GetOpenIncidentsForEscalation(ctx context.Context, minAge time.Duration) ([]*Incident, error) {
	cutoff := time.Now().UTC().Add(-minAge)
	query := `
		SELECT
			id, team_id, title, message, severity, status, source,
			alert_payload, context, analysis,
			fired_at, acknowledged_at, resolved_at, snoozed_until,
			escalation_step, created_at, updated_at, assigned_to
		FROM incidents
		WHERE status = 'open' AND fired_at < $1
		ORDER BY fired_at ASC
		LIMIT 200
	`
	rows, err := db.pool.Query(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to query escalation candidates: %w", err)
	}
	defer rows.Close()

	var incidents []*Incident
	for rows.Next() {
		incident := &Incident{}
		if err := rows.Scan(
			&incident.ID,
			&incident.TeamID,
			&incident.Title,
			&incident.Message,
			&incident.Severity,
			&incident.Status,
			&incident.Source,
			&incident.AlertPayload,
			&incident.Context,
			&incident.Analysis,
			&incident.FiredAt,
			&incident.AcknowledgedAt,
			&incident.ResolvedAt,
			&incident.SnoozedUntil,
			&incident.EscalationStep,
			&incident.CreatedAt,
			&incident.UpdatedAt,
			&incident.AssignedTo,
		); err != nil {
			return nil, fmt.Errorf("failed to scan escalation candidate: %w", err)
		}
		incidents = append(incidents, incident)
	}
	return incidents, rows.Err()
}

// IncrementEscalationStep advances the escalation_step by 1 only when the current
// value matches fromStep. Returns true if the row was updated (i.e. this caller
// "won" the escalation — prevents double-escalation with concurrent workers).
func (db *DB) IncrementEscalationStep(ctx context.Context, teamID, incidentID uuid.UUID, fromStep int) (bool, error) {
	res, err := db.pool.Exec(ctx, `
		UPDATE incidents
		SET escalation_step = escalation_step + 1, updated_at = $1
		WHERE id = $2 AND team_id = $3 AND escalation_step = $4 AND status = 'open'
	`, time.Now().UTC(), incidentID, teamID, fromStep)
	if err != nil {
		return false, fmt.Errorf("failed to increment escalation step: %w", err)
	}
	return res.RowsAffected() == 1, nil
}
