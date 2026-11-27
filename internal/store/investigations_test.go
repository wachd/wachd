package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newInvestigationFixture(t *testing.T, db *DB) (*Team, *Incident) {
	t.Helper()
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, unique("inv-team"), unique("inv-secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })
	msg := "m"
	fp := unique("fp")
	inc := &Incident{TeamID: team.ID, Title: "t", Message: &msg, Severity: "high", Status: "open", Source: "grafana",
		AlertPayload: []byte(`{}`), Fingerprint: &fp}
	if err := db.CreateIncidentWithNotificationPreparation(ctx, inc); err != nil {
		t.Fatalf("create incident: %v", err)
	}
	return team, inc
}

// backdate makes the investigation due now without waiting for the real grace.
func backdate(t *testing.T, db *DB, id uuid.UUID, sets string) {
	t.Helper()
	if _, err := db.pool.Exec(context.Background(), `UPDATE incident_investigations SET `+sets+` WHERE incident_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

// D5b: the intent is saved with the incident, with no Redis involved.
func TestInvestigation_IntentCreatedWithIncident(t *testing.T) {
	db := requireDB(t)
	team, inc := newInvestigationFixture(t, db)
	inv, err := db.GetInvestigation(context.Background(), team.ID, inc.ID)
	if err != nil || inv == nil {
		t.Fatalf("GetInvestigation: %+v, %v", inv, err)
	}
	if inv.State != InvestigationPending || inv.AttemptCount != 0 {
		t.Fatalf("unexpected initial state: %+v", inv)
	}
	if !inv.NextAttemptAt.After(time.Now()) {
		t.Error("reconciler grace period missing: a fresh job must be left to the Redis fast path first")
	}
}

// The Redis fast path can claim immediately; a second claim must not succeed.
func TestInvestigation_ClaimIsExclusive(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	_, inc := newInvestigationFixture(t, db)

	claimed, managed, attempt, err := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	if err != nil || !claimed || !managed || attempt != 1 {
		t.Fatalf("first claim: claimed=%v managed=%v attempt=%d err=%v", claimed, managed, attempt, err)
	}
	claimed, managed, _, _ = db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	if claimed || !managed {
		t.Fatalf("second claim must be refused while the lease is live (claimed=%v managed=%v)", claimed, managed)
	}
	if err := db.CompleteInvestigation(ctx, inc.ID, attempt); err != nil {
		t.Fatal(err)
	}
	if claimed, managed, _, _ = db.ClaimInvestigation(ctx, inc.ID, time.Minute); claimed || !managed {
		t.Fatal("a completed investigation must never be claimed again")
	}
}

// D5a: a crashed worker stops renewing; after the lease expires another worker
// recovers the investigation exactly once.
func TestInvestigation_ExpiredLeaseIsRecoveredOnce(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	_, inc := newInvestigationFixture(t, db)
	if claimed, _, _, _ := db.ClaimInvestigation(ctx, inc.ID, time.Minute); !claimed {
		t.Fatal("claim failed")
	}
	due, err := db.ClaimDueInvestigations(ctx, 10, 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.IncidentID == inc.ID {
			t.Fatal("a live lease must not be recovered")
		}
	}

	backdate(t, db, inc.ID, `lease_until = now() - interval '1 second'`)
	due, err = db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var got *Investigation
	for i := range due {
		if due[i].IncidentID == inc.ID {
			got = &due[i]
		}
	}
	if got == nil || got.AttemptCount != 2 {
		t.Fatalf("expected recovery as attempt 2, got %+v", got)
	}
	again, _ := db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	for _, d := range again {
		if d.IncidentID == inc.ID {
			t.Fatal("recovered investigation was claimed twice")
		}
	}
}

// D5b: an accepted alert whose Redis job was never enqueued is found by the
// reconciler once the grace period has passed, and not before.
func TestInvestigation_NeverEnqueuedIsRecoveredAfterGrace(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	_, inc := newInvestigationFixture(t, db)

	early, _ := db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	for _, d := range early {
		if d.IncidentID == inc.ID {
			t.Fatal("reconciler claimed a brand-new incident inside the grace period")
		}
	}
	backdate(t, db, inc.ID, `next_attempt_at = now() - interval '1 second'`)
	due, _ := db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	found := false
	for _, d := range due {
		found = found || d.IncidentID == inc.ID
	}
	if !found {
		t.Fatal("stranded investigation was not recovered")
	}
}

// Retries are bounded and the final failure stays visible.
func TestInvestigation_BoundedRetriesEndInVisibleFailure(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, inc := newInvestigationFixture(t, db)
	const max = 3

	for attempt := 1; attempt <= max; attempt++ {
		backdate(t, db, inc.ID, `next_attempt_at = now() - interval '1 second'`)
		claimed, _, got, err := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
		if err != nil || !claimed || got != attempt {
			t.Fatalf("attempt %d: claimed=%v got=%d err=%v", attempt, claimed, got, err)
		}
		if err := db.FailInvestigationAttempt(ctx, inc.ID, got, max, "AI analysis failed"); err != nil {
			t.Fatal(err)
		}
		inv, _ := db.GetInvestigation(ctx, team.ID, inc.ID)
		if attempt < max && (inv.State != InvestigationPending || !inv.NextAttemptAt.After(time.Now())) {
			t.Fatalf("attempt %d: want pending with backoff, got %+v", attempt, inv)
		}
		if attempt == max && inv.State != InvestigationFailed {
			t.Fatalf("want failed after %d attempts, got %+v", max, inv)
		}
	}
	inv, _ := db.GetInvestigation(ctx, team.ID, inc.ID)
	if inv.LastError == nil || *inv.LastError != "AI analysis failed" || inv.CompletedAt == nil {
		t.Errorf("failure not visible: %+v", inv)
	}
	backdate(t, db, inc.ID, `next_attempt_at = now() - interval '1 second'`)
	if claimed, _, _, _ := db.ClaimInvestigation(ctx, inc.ID, time.Minute); claimed {
		t.Fatal("a failed investigation must not be retried")
	}
}

// A worker that keeps crashing cannot loop forever: the reconciler fails the
// investigation once the attempt budget is spent.
func TestInvestigation_RepeatedCrashesEndInFailure(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, inc := newInvestigationFixture(t, db)
	backdate(t, db, inc.ID, `state='running', attempt_count=3, lease_until = now() - interval '1 second'`)

	due, err := db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.IncidentID == inc.ID {
			t.Fatal("exhausted investigation was handed out again")
		}
	}
	inv, _ := db.GetInvestigation(ctx, team.ID, inc.ID)
	if inv.State != InvestigationFailed || inv.LastError == nil {
		t.Fatalf("want visible failure, got %+v", inv)
	}
}

// A worker that lost its lease must not overwrite the new owner's state.
func TestInvestigation_StaleOwnerCannotFinalize(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, inc := newInvestigationFixture(t, db)
	_, _, first, _ := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	backdate(t, db, inc.ID, `lease_until = now() - interval '1 second'`)
	_, _, second, _ := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	if second != first+1 {
		t.Fatalf("expected takeover, attempts %d -> %d", first, second)
	}
	if ok, _ := db.RenewInvestigationLease(ctx, inc.ID, first, time.Minute); ok {
		t.Error("stale owner renewed the lease")
	}
	_ = db.CompleteInvestigation(ctx, inc.ID, first)
	if inv, _ := db.GetInvestigation(ctx, team.ID, inc.ID); inv.State != InvestigationRunning {
		t.Fatalf("stale owner changed state to %q", inv.State)
	}
}

// Graceful shutdown hands the attempt back without spending the retry budget.
func TestInvestigation_ReleaseDoesNotCountAttempt(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	team, inc := newInvestigationFixture(t, db)
	_, _, attempt, _ := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	if err := db.ReleaseInvestigation(ctx, inc.ID, attempt); err != nil {
		t.Fatal(err)
	}
	inv, _ := db.GetInvestigation(ctx, team.ID, inc.ID)
	if inv.State != InvestigationPending || inv.AttemptCount != 0 {
		t.Fatalf("want pending/0, got %+v", inv)
	}
}

// Incidents created before durable investigations existed have no row and keep
// the legacy path; they are never picked up by the reconciler.
func TestInvestigation_LegacyIncidentIsUnmanaged(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()
	_, inc := newInvestigationFixture(t, db)
	if _, err := db.pool.Exec(ctx, `DELETE FROM incident_investigations WHERE incident_id=$1`, inc.ID); err != nil {
		t.Fatal(err)
	}
	claimed, managed, _, err := db.ClaimInvestigation(ctx, inc.ID, time.Minute)
	if err != nil || claimed || managed {
		t.Fatalf("legacy incident: claimed=%v managed=%v err=%v", claimed, managed, err)
	}
	due, _ := db.ClaimDueInvestigations(ctx, 100, 3, time.Minute)
	for _, d := range due {
		if d.IncidentID == inc.ID {
			t.Fatal("legacy incident was picked up by the reconciler")
		}
	}
}
