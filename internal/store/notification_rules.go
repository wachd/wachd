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
	now := time.Now()
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

// QueuePendingNotification inserts a delayed notification into the queue.
func (db *DB) QueuePendingNotification(ctx context.Context, p *PendingNotification) error {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	_, err := db.pool.Exec(ctx, `
		INSERT INTO pending_notifications
			(id, incident_id, team_id, user_id, user_source, channel, scheduled_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, p.ID, p.IncidentID, p.TeamID, p.UserID, p.UserSource, p.Channel, p.ScheduledAt, p.CreatedAt)
	return err
}

// GetDuePendingNotifications returns notifications scheduled for now or earlier that
// have not yet been sent or cancelled. Limited to 100 per poll to avoid large batches.
func (db *DB) GetDuePendingNotifications(ctx context.Context) ([]*PendingNotification, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, incident_id, team_id, user_id, user_source, channel, scheduled_at, sent_at, cancelled_at, created_at
		FROM pending_notifications
		WHERE scheduled_at <= now() AND sent_at IS NULL AND cancelled_at IS NULL
		ORDER BY scheduled_at
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pending []*PendingNotification
	for rows.Next() {
		p := &PendingNotification{}
		if err := rows.Scan(&p.ID, &p.IncidentID, &p.TeamID, &p.UserID, &p.UserSource,
			&p.Channel, &p.ScheduledAt, &p.SentAt, &p.CancelledAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		pending = append(pending, p)
	}
	return pending, rows.Err()
}

// MarkPendingNotificationSent marks a pending notification as sent.
func (db *DB) MarkPendingNotificationSent(ctx context.Context, id uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE pending_notifications SET sent_at = now() WHERE id = $1
	`, id)
	return err
}

// CancelPendingNotificationsForIncident cancels all unsent pending notifications for an
// incident (called when the incident is acknowledged or resolved).
func (db *DB) CancelPendingNotificationsForIncident(ctx context.Context, incidentID uuid.UUID) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE pending_notifications
		SET cancelled_at = now()
		WHERE incident_id = $1 AND sent_at IS NULL AND cancelled_at IS NULL
	`, incidentID)
	return err
}
