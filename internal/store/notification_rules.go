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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListUserNotificationRules returns all notification rules for a user across all event types.
func (db *DB) ListUserNotificationRules(ctx context.Context, userID uuid.UUID, userSource string) ([]*UserNotificationRule, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, user_source, event_type, channel, delay_minutes, enabled, created_at, updated_at
		FROM user_notification_rules
		WHERE user_id = $1 AND user_source = $2
		ORDER BY event_type, delay_minutes, channel
	`, userID, userSource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*UserNotificationRule
	for rows.Next() {
		r := &UserNotificationRule{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserSource, &r.EventType, &r.Channel,
			&r.DelayMinutes, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// GetUserNotificationRules returns enabled rules for a specific user + event type.
func (db *DB) GetUserNotificationRules(ctx context.Context, userID uuid.UUID, userSource, eventType string) ([]*UserNotificationRule, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, user_source, event_type, channel, delay_minutes, enabled, created_at, updated_at
		FROM user_notification_rules
		WHERE user_id = $1 AND user_source = $2 AND event_type = $3 AND enabled = true
		ORDER BY delay_minutes, channel
	`, userID, userSource, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*UserNotificationRule
	for rows.Next() {
		r := &UserNotificationRule{}
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserSource, &r.EventType, &r.Channel,
			&r.DelayMinutes, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpsertUserNotificationRule creates or updates a notification rule.
// Conflict key: (user_id, user_source, event_type, channel, delay_minutes).
func (db *DB) UpsertUserNotificationRule(ctx context.Context, r *UserNotificationRule) (*UserNotificationRule, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	now := time.Now().UTC()
	r.CreatedAt = now
	r.UpdatedAt = now

	err := db.pool.QueryRow(ctx, `
		INSERT INTO user_notification_rules
			(id, user_id, user_source, event_type, channel, delay_minutes, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, user_source, event_type, channel, delay_minutes)
		DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at
		RETURNING id, user_id, user_source, event_type, channel, delay_minutes, enabled, created_at, updated_at
	`, r.ID, r.UserID, r.UserSource, r.EventType, r.Channel, r.DelayMinutes, r.Enabled, r.CreatedAt, r.UpdatedAt,
	).Scan(&r.ID, &r.UserID, &r.UserSource, &r.EventType, &r.Channel,
		&r.DelayMinutes, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateUserNotificationRule updates the enabled flag and delay for an existing rule.
// Enforces user ownership: only the rule owner can update it.
func (db *DB) UpdateUserNotificationRule(ctx context.Context, id, userID uuid.UUID, userSource string, enabled bool, delayMinutes int) (*UserNotificationRule, error) {
	r := &UserNotificationRule{}
	err := db.pool.QueryRow(ctx, `
		UPDATE user_notification_rules
		SET enabled = $1, delay_minutes = $2, updated_at = now()
		WHERE id = $3 AND user_id = $4 AND user_source = $5
		RETURNING id, user_id, user_source, event_type, channel, delay_minutes, enabled, created_at, updated_at
	`, enabled, delayMinutes, id, userID, userSource,
	).Scan(&r.ID, &r.UserID, &r.UserSource, &r.EventType, &r.Channel,
		&r.DelayMinutes, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteUserNotificationRule deletes a rule. Enforces user ownership.
func (db *DB) DeleteUserNotificationRule(ctx context.Context, id, userID uuid.UUID, userSource string) error {
	tag, err := db.pool.Exec(ctx, `
		DELETE FROM user_notification_rules
		WHERE id = $1 AND user_id = $2 AND user_source = $3
	`, id, userID, userSource)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ErrNotificationClaimLost means another worker owns the delivery claim, or the
// notification has already reached a terminal state.
var ErrNotificationClaimLost = errors.New("notification delivery claim lost")

const pendingNotificationColumns = `id, incident_id, team_id, user_id, user_source, channel,
	event_type, phase, state, idempotency_key, payload, scheduled_at, next_attempt_at,
	attempt_count, max_attempts, claimed_at, claim_token, last_attempt_at, last_error,
	sent_at, cancelled_at, created_at`

const pendingNotificationReturningColumns = `p.id, p.incident_id, p.team_id, p.user_id, p.user_source, p.channel,
	p.event_type, p.phase, p.state, p.idempotency_key, p.payload, p.scheduled_at, p.next_attempt_at,
	p.attempt_count, p.max_attempts, p.claimed_at, p.claim_token, p.last_attempt_at, p.last_error,
	p.sent_at, p.cancelled_at, p.created_at`

func scanPendingNotification(row pgx.Row) (*PendingNotification, error) {
	p := &PendingNotification{}
	err := row.Scan(&p.ID, &p.IncidentID, &p.TeamID, &p.UserID, &p.UserSource, &p.Channel,
		&p.EventType, &p.Phase, &p.State, &p.IdempotencyKey, &p.Payload, &p.ScheduledAt,
		&p.NextAttemptAt, &p.AttemptCount, &p.MaxAttempts, &p.ClaimedAt, &p.ClaimToken,
		&p.LastAttemptAt, &p.LastError, &p.SentAt, &p.CancelledAt, &p.CreatedAt)
	return p, err
}

func notificationIdempotencyKey(p *PendingNotification) string {
	scope := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", p.IncidentID, p.UserID, p.Channel, p.EventType, p.Phase)
	sum := sha256.Sum256([]byte(scope))
	return hex.EncodeToString(sum[:])
}

// QueuePendingNotification persists a delivery intent before any provider call.
// The idempotency scope is incident, recipient, channel, event type, and phase.
func (db *DB) QueuePendingNotification(ctx context.Context, p *PendingNotification) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.EventType == "" {
		p.EventType = "new_alert"
	}
	if p.Phase == "" {
		p.Phase = "initial"
	}
	if p.State == "" {
		p.State = "pending"
	}
	if p.ScheduledAt.IsZero() {
		p.ScheduledAt = time.Now().UTC()
	}
	if p.NextAttemptAt.IsZero() {
		p.NextAttemptAt = p.ScheduledAt
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 5
	}
	if p.IdempotencyKey == "" {
		p.IdempotencyKey = notificationIdempotencyKey(p)
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	_, err := db.pool.Exec(ctx, `
		INSERT INTO pending_notifications
			(id, incident_id, team_id, user_id, user_source, channel, event_type, phase,
			 state, idempotency_key, payload, scheduled_at, next_attempt_at, max_attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
	`, p.ID, p.IncidentID, p.TeamID, p.UserID, p.UserSource, p.Channel, p.EventType, p.Phase,
		p.State, p.IdempotencyKey, p.Payload, p.ScheduledAt, p.NextAttemptAt, p.MaxAttempts, p.CreatedAt)
	return err
}

// ClaimDuePendingNotifications atomically leases due work. Expired leases are
// reclaimable after claimTTL, so a process crash cannot permanently lose work.
func (db *DB) ClaimDuePendingNotifications(ctx context.Context, limit int, claimTTL time.Duration) ([]*PendingNotification, error) {
	if limit <= 0 {
		limit = 100
	}
	if claimTTL <= 0 {
		claimTTL = 2 * time.Minute
	}
	claimToken := uuid.New()
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT id FROM pending_notifications
			WHERE COALESCE(next_attempt_at, scheduled_at) <= now()
			  AND sent_at IS NULL AND cancelled_at IS NULL
			  AND attempt_count < max_attempts
			  AND state IN ('pending', 'retrying', 'processing')
			  AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => $1::double precision))
			ORDER BY COALESCE(next_attempt_at, scheduled_at), created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE pending_notifications p
		SET state = 'processing', claimed_at = now(), claim_token = $3,
			attempt_count = p.attempt_count + 1, last_attempt_at = now(),
			next_attempt_at = COALESCE(p.next_attempt_at, p.scheduled_at)
		FROM due WHERE p.id = due.id
		RETURNING `+pendingNotificationReturningColumns,
		claimTTL.Seconds(), limit, claimToken)
	if err != nil {
		return nil, err
	}

	var pending []*PendingNotification
	for rows.Next() {
		p, err := scanPendingNotification(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pending, tx.Commit(ctx)
}

// MarkPendingNotificationSent records success only for the active lease.
func (db *DB) MarkPendingNotificationSent(ctx context.Context, id, claimToken uuid.UUID) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE pending_notifications SET state = 'delivered', sent_at = now(),
			claimed_at = NULL, claim_token = NULL, last_error = NULL
		WHERE id = $1 AND claim_token = $2 AND state = 'processing'
	`, id, claimToken)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotificationClaimLost
	}
	return err
}

// MarkPendingNotificationFailed records a sanitized provider error and either
// schedules another attempt or moves the notification to a terminal state.
func (db *DB) MarkPendingNotificationFailed(ctx context.Context, id, claimToken uuid.UUID, retryAt time.Time, failure string, permanent bool) error {
	tag, err := db.pool.Exec(ctx, `
		UPDATE pending_notifications
		SET state = CASE WHEN $5 OR attempt_count >= max_attempts THEN 'failed' ELSE 'retrying' END,
			next_attempt_at = CASE WHEN $5 OR attempt_count >= max_attempts THEN next_attempt_at ELSE $3 END,
			last_error = $4, claimed_at = NULL, claim_token = NULL
		WHERE id = $1 AND claim_token = $2 AND state = 'processing'
	`, id, claimToken, retryAt, failure, permanent)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotificationClaimLost
	}
	return err
}

// GetPendingNotification returns one delivery record for diagnostics and tests.
func (db *DB) GetPendingNotification(ctx context.Context, id uuid.UUID) (*PendingNotification, error) {
	return scanPendingNotification(db.pool.QueryRow(ctx, `SELECT `+pendingNotificationColumns+` FROM pending_notifications WHERE id = $1`, id))
}

// CancelPendingNotificationsForIncident cancels all unsent pending notifications for an
// incident (called when the incident is acknowledged or resolved).
func (db *DB) CancelPendingNotificationsForIncident(ctx context.Context, incidentID uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE pending_notifications
		SET state = 'cancelled', cancelled_at = now(), claimed_at = NULL, claim_token = NULL
		WHERE incident_id = $1 AND sent_at IS NULL AND cancelled_at IS NULL
	`, incidentID)
	return err
}
