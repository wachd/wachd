package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNotificationDeliveryStateMachine(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, unique("notification-team"), unique("notification-secret"))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	userID := uuid.New()
	incident := &Incident{TeamID: team.ID, Title: unique("notification-incident"), Severity: "high", Status: "open", Source: "phase5a", AlertPayload: []byte(`{"test":true}`)}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("create incident: %v", err)
	}
	incidentID := incident.ID
	p := &PendingNotification{
		IncidentID:  incidentID,
		TeamID:      team.ID,
		UserID:      userID,
		UserSource:  "local",
		Channel:     "email",
		EventType:   "new_alert",
		Phase:       "initial",
		Payload:     []byte(`{"safe":"payload"}`),
		ScheduledAt: time.Now().UTC().Add(-time.Second),
		MaxAttempts: 3,
	}
	if err := db.QueuePendingNotification(ctx, p); err != nil {
		t.Fatalf("queue notification: %v", err)
	}
	duplicate := *p
	duplicate.ID = uuid.New()
	if err := db.QueuePendingNotification(ctx, &duplicate); err != nil {
		t.Fatalf("queue duplicate notification: %v", err)
	}
	var count int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM pending_notifications WHERE idempotency_key = $1`, p.IdempotencyKey).Scan(&count); err != nil {
		t.Fatalf("count idempotent intents: %v", err)
	}
	if count != 1 {
		t.Fatalf("idempotency rows = %d, want 1", count)
	}

	claimed, err := db.ClaimDuePendingNotifications(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim notification: count=%d err=%v", len(claimed), err)
	}
	claim := claimed[0]
	if claim.AttemptCount != 1 || claim.ClaimToken == nil || claim.State != "processing" {
		t.Fatalf("claim state: attempts=%d token=%v state=%q", claim.AttemptCount, claim.ClaimToken, claim.State)
	}
	claimedAgain, err := db.ClaimDuePendingNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("second worker claimed %d active notification(s)", len(claimedAgain))
	}

	retryAt := time.Now().UTC().Add(time.Hour)
	if err := db.MarkPendingNotificationFailed(ctx, claim.ID, *claim.ClaimToken, retryAt, "smtp password=[REDACTED]", false); err != nil {
		t.Fatalf("record retryable failure: %v", err)
	}
	got, err := db.GetPendingNotification(ctx, claim.ID)
	if err != nil {
		t.Fatalf("get retryable notification: %v", err)
	}
	if got.State != "retrying" || got.AttemptCount != 1 || got.NextAttemptAt.Before(retryAt.Add(-time.Second)) || got.LastError == nil {
		t.Fatalf("retry state: state=%q attempts=%d next=%v error=%v", got.State, got.AttemptCount, got.NextAttemptAt, got.LastError)
	}
	if *got.LastError != "smtp password=[REDACTED]" {
		t.Fatalf("unexpected persisted error: %q", *got.LastError)
	}

	got.NextAttemptAt = time.Now().UTC().Add(-time.Second)
	if _, err := db.Pool().Exec(ctx, `UPDATE pending_notifications SET next_attempt_at = $1 WHERE id = $2`, got.NextAttemptAt, got.ID); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	retried, err := db.ClaimDuePendingNotifications(ctx, 10, time.Minute)
	if err != nil || len(retried) != 1 {
		t.Fatalf("claim retry: count=%d err=%v", len(retried), err)
	}
	if retried[0].AttemptCount != 2 {
		t.Fatalf("retry attempt count=%d, want 2", retried[0].AttemptCount)
	}
	if err := db.MarkPendingNotificationSent(ctx, retried[0].ID, *retried[0].ClaimToken); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	got, err = db.GetPendingNotification(ctx, p.ID)
	if err != nil {
		t.Fatalf("get delivered notification: %v", err)
	}
	if got.State != "delivered" || got.SentAt == nil || got.ClaimToken != nil {
		t.Fatalf("delivered state: state=%q sent=%v token=%v", got.State, got.SentAt, got.ClaimToken)
	}
}

func TestNotificationClaimRecoveryAndCancellation(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, unique("notification-recovery-team"), unique("notification-recovery-secret"))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	newIntent := func() *PendingNotification {
		incident := &Incident{TeamID: team.ID, Title: unique("notification-recovery-incident"), Severity: "high", Status: "open", Source: "phase5a", AlertPayload: []byte(`{"test":true}`)}
		if err := db.CreateIncident(ctx, incident); err != nil {
			t.Fatalf("create recovery incident: %v", err)
		}
		p := &PendingNotification{IncidentID: incident.ID, TeamID: team.ID, UserID: uuid.New(), UserSource: "local", Channel: "email", ScheduledAt: time.Now().UTC().Add(-time.Second)}
		if err := db.QueuePendingNotification(ctx, p); err != nil {
			t.Fatalf("queue intent: %v", err)
		}
		return p
	}

	crashIntent := newIntent()
	first, err := db.ClaimDuePendingNotifications(ctx, 10, time.Microsecond)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial recovery claim: count=%d err=%v", len(first), err)
	}
	second, err := db.ClaimDuePendingNotifications(ctx, 10, time.Microsecond)
	if err != nil || len(second) != 1 || second[0].ID != crashIntent.ID {
		t.Fatalf("expired claim recovery: count=%d err=%v", len(second), err)
	}

	cancelIntent := newIntent()
	if err := db.CancelPendingNotificationsForIncident(ctx, cancelIntent.IncidentID); err != nil {
		t.Fatalf("cancel intent: %v", err)
	}
	claimed, err := db.ClaimDuePendingNotifications(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim after cancellation: %v", err)
	}
	for _, p := range claimed {
		if p.ID == cancelIntent.ID {
			t.Fatalf("cancelled notification was claimable")
		}
	}
}

func TestNotificationClaimsAreExclusiveAcrossWorkers(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, unique("notification-concurrency-team"), unique("notification-concurrency-secret"))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })
	for i := 0; i < 2; i++ {
		incident := &Incident{TeamID: team.ID, Title: unique("notification-concurrency-incident"), Severity: "high", Status: "open", Source: "phase5a", AlertPayload: []byte(`{"test":true}`)}
		if err := db.CreateIncident(ctx, incident); err != nil {
			t.Fatalf("create concurrency incident: %v", err)
		}
		p := &PendingNotification{IncidentID: incident.ID, TeamID: team.ID, UserID: uuid.New(), UserSource: "local", Channel: "email", ScheduledAt: time.Now().UTC().Add(-time.Second)}
		if err := db.QueuePendingNotification(ctx, p); err != nil {
			t.Fatalf("queue intent %d: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := db.ClaimDuePendingNotifications(ctx, 1, time.Minute)
			if err != nil {
				t.Errorf("concurrent claim: %v", err)
				return
			}
			results <- len(claimed)
		}()
	}
	wg.Wait()
	close(results)
	total := 0
	for n := range results {
		total += n
	}
	if total != 2 {
		t.Fatalf("concurrent workers claimed %d notifications, want 2", total)
	}
}

func TestIncidentNotificationPreparationIsDurable(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, unique("prep-team"), unique("prep-secret"))
	if err != nil {
		t.Fatal(err)
	}
	msg := "atomic preparation"
	inc := &Incident{TeamID: team.ID, Title: unique("prep"), Message: &msg, Severity: "high", Status: "open", Source: "test", AlertPayload: []byte(`{"alerts":[]}`)}
	if err := db.CreateIncidentWithNotificationPreparation(ctx, inc); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM incident_notification_preparations WHERE incident_id=$1 AND processed_at IS NULL`, inc.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one pending preparation, got %d", count)
	}
	claimed, err := db.ClaimNotificationPreparations(ctx, 10, time.Minute)
	if err != nil || !containsPreparation(claimed, inc.ID) {
		t.Fatalf("claim preparation: %v len=%d", err, len(claimed))
	}
	if err := db.MarkNotificationPreparationFailed(ctx, inc.ID, "no on-call assignment"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE incident_notification_preparations SET next_attempt_at=now() WHERE incident_id=$1`, inc.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = db.ClaimNotificationPreparations(ctx, 10, time.Minute)
	if err != nil || !containsPreparation(claimed, inc.ID) {
		t.Fatalf("reclaim preparation: %v len=%d", err, len(claimed))
	}
	if err := db.MarkNotificationPreparationProcessed(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM incident_notification_preparations WHERE incident_id=$1 AND processed_at IS NOT NULL`, inc.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected processed preparation, got %d", count)
	}
}

func containsPreparation(items []NotificationPreparation, id uuid.UUID) bool {
	for _, item := range items {
		if item.IncidentID == id {
			return true
		}
	}
	return false
}
