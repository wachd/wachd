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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Investigation states.
const (
	InvestigationPending   = "pending"
	InvestigationRunning   = "running"
	InvestigationCompleted = "completed"
	InvestigationFailed    = "failed"
)

// InvestigationFirstAttemptGrace is how long the Redis fast path has to deliver
// a brand-new job before the reconciler considers the incident stranded.
const InvestigationFirstAttemptGrace = 30 * time.Second

// Investigation is the durable state of one incident's root-cause investigation.
type Investigation struct {
	IncidentID    uuid.UUID  `json:"incident_id"`
	TeamID        uuid.UUID  `json:"team_id"`
	State         string     `json:"state"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

// InvestigationStatus is the part of an investigation shown through the API.
type InvestigationStatus struct {
	State        string  `json:"state"` // pending | running | completed | failed
	AttemptCount int     `json:"attempt_count"`
	LastError    *string `json:"last_error,omitempty"`
}

// Status returns the API view of the investigation.
func (inv *Investigation) Status() *InvestigationStatus {
	if inv == nil {
		return nil
	}
	return &InvestigationStatus{State: inv.State, AttemptCount: inv.AttemptCount, LastError: inv.LastError}
}

// ClaimInvestigation takes the lease for one incident. managed is false when the
// incident has no investigation row (created before durable intents existed),
// in which case the caller processes it the legacy way. When managed is true and
// claimed is false, the investigation is finished, failed, leased by another
// worker or waiting out a retry backoff, and the caller must skip it.
func (db *DB) ClaimInvestigation(ctx context.Context, incidentID uuid.UUID, lease time.Duration) (claimed, managed bool, attempt int, err error) {
	err = db.pool.QueryRow(ctx, `
		UPDATE incident_investigations
		SET state='running', attempt_count=attempt_count+1,
		    lease_until=now() + make_interval(secs => $2::double precision), updated_at=now()
		WHERE incident_id=$1
		  AND ((state='pending' AND (attempt_count=0 OR next_attempt_at <= now()))
		    OR (state='running' AND lease_until < now()))
		RETURNING attempt_count`, incidentID, lease.Seconds()).Scan(&attempt)
	if err == nil {
		return true, true, attempt, nil
	}
	if err != pgx.ErrNoRows {
		return false, false, 0, err
	}
	err = db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM incident_investigations WHERE incident_id=$1)`, incidentID).Scan(&managed)
	return false, managed, 0, err
}

// ClaimDueInvestigations leases up to limit stranded investigations: pending
// ones whose first-attempt grace or retry backoff has passed, and running ones
// whose worker stopped renewing the lease. Investigations that have used all
// their attempts are marked failed instead of being claimed again.
func (db *DB) ClaimDueInvestigations(ctx context.Context, limit, maxAttempts int, lease time.Duration) ([]Investigation, error) {
	if _, err := db.pool.Exec(ctx, `
		UPDATE incident_investigations
		SET state='failed', completed_at=now(), lease_until=NULL, updated_at=now(),
		    last_error=COALESCE(last_error, 'worker stopped before the investigation finished')
		WHERE state='running' AND lease_until < now() AND attempt_count >= $1`, maxAttempts); err != nil {
		return nil, err
	}
	rows, err := db.pool.Query(ctx, `
		WITH due AS (
			SELECT incident_id FROM incident_investigations
			WHERE (state='pending' AND next_attempt_at <= now())
			   OR (state='running' AND lease_until < now())
			ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE incident_investigations i
		SET state='running', attempt_count=i.attempt_count+1,
		    lease_until=now() + make_interval(secs => $2::double precision), updated_at=now()
		FROM due WHERE i.incident_id=due.incident_id
		RETURNING i.incident_id, i.team_id, i.state, i.attempt_count, i.next_attempt_at`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Investigation
	for rows.Next() {
		var inv Investigation
		if err := rows.Scan(&inv.IncidentID, &inv.TeamID, &inv.State, &inv.AttemptCount, &inv.NextAttemptAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RenewInvestigationLease extends a running lease. It reports false if the lease
// was lost (another worker took over or the row is no longer running).
func (db *DB) RenewInvestigationLease(ctx context.Context, incidentID uuid.UUID, attempt int, lease time.Duration) (bool, error) {
	tag, err := db.pool.Exec(ctx, `
		UPDATE incident_investigations
		SET lease_until=now() + make_interval(secs => $3::double precision), updated_at=now()
		WHERE incident_id=$1 AND state='running' AND attempt_count=$2`, incidentID, attempt, lease.Seconds())
	return tag.RowsAffected() == 1, err
}

// CompleteInvestigation marks a finished investigation. The attempt guard keeps
// a worker that lost its lease from overwriting the current owner's state.
func (db *DB) CompleteInvestigation(ctx context.Context, incidentID uuid.UUID, attempt int) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE incident_investigations
		SET state='completed', completed_at=now(), lease_until=NULL, last_error=NULL, updated_at=now()
		WHERE incident_id=$1 AND state='running' AND attempt_count=$2`, incidentID, attempt)
	return err
}

// FailInvestigationAttempt records a failed attempt. The investigation goes back
// to pending with exponential backoff (10s, 20s, 40s ... capped at 5 minutes,
// +/-20% jitter) until maxAttempts is reached, then becomes failed so the
// problem is visible instead of silently retried forever.
func (db *DB) FailInvestigationAttempt(ctx context.Context, incidentID uuid.UUID, attempt, maxAttempts int, failure string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE incident_investigations
		SET state = CASE WHEN attempt_count >= $3 THEN 'failed' ELSE 'pending' END,
		    completed_at = CASE WHEN attempt_count >= $3 THEN now() ELSE NULL END,
		    lease_until=NULL, last_error=$4, updated_at=now(),
		    next_attempt_at = now() + make_interval(secs => LEAST(300.0,
		        10.0 * power(2.0, GREATEST(attempt_count - 1, 0))) * (0.8 + random() * 0.4))
		WHERE incident_id=$1 AND state='running' AND attempt_count=$2`, incidentID, attempt, maxAttempts, failure)
	return err
}

// ReleaseInvestigation hands an interrupted attempt back (graceful shutdown)
// without counting it against the retry budget.
func (db *DB) ReleaseInvestigation(ctx context.Context, incidentID uuid.UUID, attempt int) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE incident_investigations
		SET state='pending', attempt_count=GREATEST(attempt_count-1, 0), next_attempt_at=now(),
		    lease_until=NULL, updated_at=now()
		WHERE incident_id=$1 AND state='running' AND attempt_count=$2`, incidentID, attempt)
	return err
}

// GetInvestigation returns the investigation state for an incident, or nil when
// the incident predates durable investigations.
func (db *DB) GetInvestigation(ctx context.Context, teamID, incidentID uuid.UUID) (*Investigation, error) {
	var inv Investigation
	err := db.pool.QueryRow(ctx, `
		SELECT incident_id, team_id, state, attempt_count, next_attempt_at, lease_until, last_error, completed_at
		FROM incident_investigations WHERE incident_id=$1 AND team_id=$2`, incidentID, teamID).
		Scan(&inv.IncidentID, &inv.TeamID, &inv.State, &inv.AttemptCount, &inv.NextAttemptAt, &inv.LeaseUntil, &inv.LastError, &inv.CompletedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &inv, err
}
