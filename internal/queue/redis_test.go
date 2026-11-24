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

package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// requireQueue connects to the test Redis instance.
// Skips the test if Redis is not available.
func requireQueue(t *testing.T) *Queue {
	t.Helper()
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/1"
	}
	queueKey := "wachd:test:internal-queue:" + t.Name() + ":alerts"
	q, err := NewQueueWithKey(redisURL, queueKey)
	if err != nil {
		t.Skipf("skipping integration test: Redis unavailable (%v) — run make docker-up", err)
	}
	if err := q.client.Del(context.Background(), queueKey).Err(); err != nil {
		_ = q.Close()
		t.Fatalf("clear owned queue key: %v", err)
	}
	t.Cleanup(func() {
		_ = q.client.Del(context.Background(), queueKey).Err()
		_ = q.Close()
	})
	return q
}

// ── NewQueue ──────────────────────────────────────────────────────────────────

func TestNewQueue_InvalidURL(t *testing.T) {
	_, err := NewQueue("not-a-valid-redis-url")
	if err == nil {
		t.Error("expected error for invalid Redis URL")
	}
}

func TestNewQueue_Valid(t *testing.T) {
	q := requireQueue(t)
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
}

func TestNewQueue_DefaultKeyUnchanged(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/1"
	}
	q, err := NewQueue(redisURL)
	if err != nil {
		t.Skipf("skipping integration test: Redis unavailable (%v) — run make docker-up", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	if q.queueKey != alertQueueKey {
		t.Errorf("default queue key changed: got %q, want %q", q.queueKey, alertQueueKey)
	}
}

func TestQueueKeysAreIsolated(t *testing.T) {
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/1"
	}
	keyA := "wachd:test:internal-queue:" + t.Name() + ":a"
	keyB := "wachd:test:internal-queue:" + t.Name() + ":b"
	qA, err := NewQueueWithKey(redisURL, keyA)
	if err != nil {
		t.Skipf("skipping integration test: Redis unavailable (%v) — run make docker-up", err)
	}
	qB, err := NewQueueWithKey(redisURL, keyB)
	if err != nil {
		_ = qA.Close()
		t.Fatalf("create second isolated queue: %v", err)
	}
	ctx := context.Background()
	if err := qA.client.Del(ctx, keyA, keyB).Err(); err != nil {
		_ = qA.Close()
		_ = qB.Close()
		t.Fatalf("clear owned queue keys: %v", err)
	}
	t.Cleanup(func() {
		_ = qA.client.Del(context.Background(), keyA, keyB).Err()
		_ = qA.Close()
		_ = qB.Close()
	})

	teamA, teamB := uuid.New(), uuid.New()
	if err := qA.EnqueueAlert(ctx, uuid.New(), teamA, []byte(`{"owner":"a"}`)); err != nil {
		t.Fatalf("enqueue A: %v", err)
	}
	if err := qB.EnqueueAlert(ctx, uuid.New(), teamB, []byte(`{"owner":"b"}`)); err != nil {
		t.Fatalf("enqueue B: %v", err)
	}

	jobA, err := qA.DequeueAlert(ctx, time.Second)
	if err != nil || jobA == nil {
		t.Fatalf("dequeue A: job=%v err=%v", jobA != nil, err)
	}
	jobB, err := qB.DequeueAlert(ctx, time.Second)
	if err != nil || jobB == nil {
		t.Fatalf("dequeue B: job=%v err=%v", jobB != nil, err)
	}
	if jobA.TeamID != teamA || jobB.TeamID != teamB {
		t.Error("isolated queues consumed another queue's job")
	}
}

// ── EnqueueAlert / DequeueAlert ───────────────────────────────────────────────

func TestEnqueueAndDequeue(t *testing.T) {
	q := requireQueue(t)
	ctx := context.Background()

	incidentID := uuid.New()
	teamID := uuid.New()
	payload := []byte(`{"alertname":"TestAlert","severity":"high"}`)

	// The helper provides an empty test-owned queue key.
	before, err := q.GetQueueLength(ctx)
	if err != nil {
		t.Fatalf("GetQueueLength: %v", err)
	}

	// Enqueue
	if err := q.EnqueueAlert(ctx, incidentID, teamID, payload); err != nil {
		t.Fatalf("EnqueueAlert: %v", err)
	}

	// Verify queue length increased
	after, err := q.GetQueueLength(ctx)
	if err != nil {
		t.Fatalf("GetQueueLength after enqueue: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected queue length %d, got %d", before+1, after)
	}

	// Dequeue
	job, err := q.DequeueAlert(ctx, time.Second)
	if err != nil {
		t.Fatalf("DequeueAlert: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}

	// Verify job fields
	if job.Type != jobTypeAlert {
		t.Errorf("expected type 'alert', got %q", job.Type)
	}
	if job.IncidentID != incidentID {
		t.Errorf("expected incidentID %v, got %v", incidentID, job.IncidentID)
	}
	if job.TeamID != teamID {
		t.Errorf("expected teamID %v, got %v", teamID, job.TeamID)
	}
	if string(job.Payload) != string(payload) {
		t.Errorf("payload mismatch: got %q", job.Payload)
	}
	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestEnqueueResolvedIncidentJob(t *testing.T) {
	q := requireQueue(t)
	ctx := context.Background()

	incidentID := uuid.New()
	teamID := uuid.New()

	if err := q.EnqueueIncidentResolved(ctx, incidentID, teamID); err != nil {
		t.Fatalf("EnqueueIncidentResolved: %v", err)
	}

	job, err := q.DequeueAlert(ctx, time.Second)
	if err != nil {
		t.Fatalf("DequeueAlert: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}
	if job.Type != jobTypeIncidentResolved {
		t.Fatalf("expected type %q, got %q", jobTypeIncidentResolved, job.Type)
	}
	if len(job.Payload) != 0 {
		t.Fatalf("expected empty payload for resolved job, got %q", job.Payload)
	}
}

func TestEnqueueMultiple(t *testing.T) {
	q := requireQueue(t)
	ctx := context.Background()

	teamID := uuid.New()

	// Enqueue 3 jobs
	for i := 0; i < 3; i++ {
		if err := q.EnqueueAlert(ctx, uuid.New(), teamID, []byte(`{}`)); err != nil {
			t.Fatalf("EnqueueAlert %d: %v", i, err)
		}
	}

	// Drain 3 jobs
	for i := 0; i < 3; i++ {
		job, err := q.DequeueAlert(ctx, time.Second)
		if err != nil {
			t.Fatalf("DequeueAlert %d: %v", i, err)
		}
		if job == nil {
			t.Fatalf("expected job %d, got nil", i)
		}
		if job.TeamID != teamID {
			t.Errorf("job %d: expected teamID %v, got %v", i, teamID, job.TeamID)
		}
	}
}

func TestDequeue_Timeout_NoJob(t *testing.T) {
	q := requireQueue(t)
	ctx := context.Background()

	// Use a very short timeout — the test-owned queue is empty.
	job, err := q.DequeueAlert(ctx, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("DequeueAlert timeout: unexpected error: %v", err)
	}
	if job != nil {
		t.Fatal("expected no job from an empty isolated queue")
	}
}

// ── GetQueueLength ────────────────────────────────────────────────────────────

func TestGetQueueLength(t *testing.T) {
	q := requireQueue(t)
	ctx := context.Background()

	before, err := q.GetQueueLength(ctx)
	if err != nil {
		t.Fatalf("GetQueueLength: %v", err)
	}

	// Enqueue and check
	_ = q.EnqueueAlert(ctx, uuid.New(), uuid.New(), []byte(`{}`))

	after, err := q.GetQueueLength(ctx)
	if err != nil {
		t.Fatalf("GetQueueLength after enqueue: %v", err)
	}
	if after < before+1 {
		t.Errorf("expected length >= %d, got %d", before+1, after)
	}

	// Drain the job we added
	_, _ = q.DequeueAlert(ctx, time.Second)
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestQueue_Close(t *testing.T) {
	q := requireQueue(t)
	// Close should not error
	if err := q.Close(); err != nil {
		t.Errorf("Close: unexpected error: %v", err)
	}
	// Double-close should not panic (may return error — that's fine)
	_ = q.Close()
}
