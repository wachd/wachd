package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/queue"
	"github.com/wachd/wachd/internal/store"
)

type fakeInvs struct {
	mu        sync.Mutex
	claimed   bool
	managed   bool
	attempt   int
	completed int
	failed    []string
	released  int
	renewed   int
	due       []store.Investigation
}

func (f *fakeInvs) ClaimInvestigation(context.Context, uuid.UUID, time.Duration) (bool, bool, int, error) {
	return f.claimed, f.managed, f.attempt, nil
}
func (f *fakeInvs) ClaimDueInvestigations(context.Context, int, int, time.Duration) ([]store.Investigation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.due
	f.due = nil
	return d, nil
}
func (f *fakeInvs) RenewInvestigationLease(context.Context, uuid.UUID, int, time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewed++
	return true, nil
}
func (f *fakeInvs) CompleteInvestigation(context.Context, uuid.UUID, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed++
	return nil
}
func (f *fakeInvs) FailInvestigationAttempt(_ context.Context, _ uuid.UUID, _, _ int, failure string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, failure)
	return nil
}
func (f *fakeInvs) ReleaseInvestigation(context.Context, uuid.UUID, int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released++
	return nil
}

func TestRunInvestigation_SuccessCompletes(t *testing.T) {
	f := &fakeInvs{}
	w := &Worker{invs: f, investigate: func(context.Context, uuid.UUID, uuid.UUID) error { return nil }}
	if err := w.runInvestigation(context.Background(), uuid.New(), uuid.New(), 1); err != nil {
		t.Fatal(err)
	}
	if f.completed != 1 || len(f.failed) != 0 {
		t.Fatalf("completed=%d failed=%v", f.completed, f.failed)
	}
}

// A failed AI call is recorded for retry instead of being silently dropped.
func TestRunInvestigation_FailureIsRecorded(t *testing.T) {
	f := &fakeInvs{}
	w := &Worker{invs: f, investigate: func(context.Context, uuid.UUID, uuid.UUID) error { return errors.New("AI analysis failed: boom") }}
	_ = w.runInvestigation(context.Background(), uuid.New(), uuid.New(), 1)
	if f.completed != 0 || len(f.failed) != 1 {
		t.Fatalf("completed=%d failed=%v", f.completed, f.failed)
	}
}

// Shutdown mid-investigation hands the work back; it is not marked failed.
func TestRunInvestigation_ShutdownReleases(t *testing.T) {
	f := &fakeInvs{}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Worker{invs: f, investigate: func(c context.Context, _, _ uuid.UUID) error {
		cancel()
		<-c.Done()
		return c.Err()
	}}
	_ = w.runInvestigation(ctx, uuid.New(), uuid.New(), 1)
	if f.released != 1 || len(f.failed) != 0 || f.completed != 0 {
		t.Fatalf("released=%d failed=%v completed=%d", f.released, f.failed, f.completed)
	}
}

// D5a/D5b: the reconciler runs whatever it claims, with the claimed attempt.
func TestReconcile_RunsClaimedInvestigations(t *testing.T) {
	id := uuid.New()
	f := &fakeInvs{due: []store.Investigation{{IncidentID: id, TeamID: uuid.New(), AttemptCount: 2}}}
	ran := make(chan uuid.UUID, 1)
	w := &Worker{invs: f, investigate: func(_ context.Context, _, inc uuid.UUID) error { ran <- inc; return nil }}
	w.reconcileInvestigations(context.Background())
	select {
	case got := <-ran:
		if got != id {
			t.Fatalf("ran %s, want %s", got, id)
		}
	default:
		t.Fatal("reconciler did not run the claimed investigation")
	}
	if f.completed != 1 {
		t.Fatalf("completed=%d", f.completed)
	}
}

// A duplicate Redis job for work that is finished or leased elsewhere is a no-op.
func TestProcessAlertJob_DuplicateJobIsSkipped(t *testing.T) {
	f := &fakeInvs{claimed: false, managed: true}
	called := false
	w := &Worker{invs: f, investigate: func(context.Context, uuid.UUID, uuid.UUID) error { called = true; return nil }}
	if err := w.processAlertJob(context.Background(), jobFor(uuid.New(), uuid.New())); err != nil {
		t.Fatal(err)
	}
	if called || f.completed != 0 {
		t.Fatal("duplicate job must not be investigated again")
	}
}

func jobFor(teamID, incidentID uuid.UUID) *queue.Job {
	return &queue.Job{ID: "j", Type: "alert", TeamID: teamID, IncidentID: incidentID}
}
