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

package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

const (
	// investigationLease is how long a worker owns an investigation without
	// renewing it. A crashed worker is therefore detected within this time.
	investigationLease = 45 * time.Second
	// investigationHeartbeat renews the lease while an investigation runs.
	investigationHeartbeat = 15 * time.Second
	// maxInvestigationAttempts bounds retries; after that the investigation is
	// marked failed and stays visible on the incident.
	maxInvestigationAttempts = 3
	// investigationReconcileEvery is how often stranded investigations are looked for.
	investigationReconcileEvery = 5 * time.Second
	// investigationReconcileBatch bounds the work the reconciler starts per pass.
	investigationReconcileBatch = 2
)

// investigationStore is the durable investigation state the worker needs.
// *store.DB implements it.
type investigationStore interface {
	ClaimInvestigation(ctx context.Context, incidentID uuid.UUID, lease time.Duration) (claimed, managed bool, attempt int, err error)
	ClaimDueInvestigations(ctx context.Context, limit, maxAttempts int, lease time.Duration) ([]store.Investigation, error)
	RenewInvestigationLease(ctx context.Context, incidentID uuid.UUID, attempt int, lease time.Duration) (bool, error)
	CompleteInvestigation(ctx context.Context, incidentID uuid.UUID, attempt int) error
	FailInvestigationAttempt(ctx context.Context, incidentID uuid.UUID, attempt, maxAttempts int, failure string) error
	ReleaseInvestigation(ctx context.Context, incidentID uuid.UUID, attempt int) error
}

func (w *Worker) investigationStore() investigationStore {
	if w.invs != nil {
		return w.invs
	}
	return w.db
}

// runInvestigation runs one claimed attempt: it keeps the lease alive, then
// records completion, a retryable failure, or a graceful hand-back on shutdown.
func (w *Worker) runInvestigation(ctx context.Context, teamID, incidentID uuid.UUID, attempt int) error {
	invs := w.investigationStore()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(investigationHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				ok, err := invs.RenewInvestigationLease(runCtx, incidentID, attempt, investigationLease)
				if err == nil && !ok {
					log.Printf("investigation %s lost its lease, stopping attempt %d", incidentID, attempt)
					cancel()
					return
				}
			}
		}
	}()

	investigate := w.investigateIncident
	if w.investigate != nil {
		investigate = w.investigate
	}
	err := investigate(runCtx, teamID, incidentID)
	cancel()
	wg.Wait()

	// The caller's context may already be cancelled (shutdown), so state
	// changes use their own short deadline.
	fin, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	switch {
	case err == nil:
		if cerr := invs.CompleteInvestigation(fin, incidentID, attempt); cerr != nil {
			log.Printf("investigation %s: mark completed: %v", incidentID, cerr)
		}
	case ctx.Err() != nil:
		log.Printf("investigation %s interrupted by shutdown, handing back", incidentID)
		if rerr := invs.ReleaseInvestigation(fin, incidentID, attempt); rerr != nil {
			log.Printf("investigation %s: release: %v", incidentID, rerr)
		}
	default:
		failure := sanitizeNotificationError(err)
		if ferr := invs.FailInvestigationAttempt(fin, incidentID, attempt, maxInvestigationAttempts, failure); ferr != nil {
			log.Printf("investigation %s: record failure: %v", incidentID, ferr)
		}
		if attempt >= maxInvestigationAttempts {
			log.Printf("✗ investigation %s failed permanently after %d attempts: %s", incidentID, attempt, failure)
		} else {
			log.Printf("investigation %s attempt %d failed, will retry: %s", incidentID, attempt, failure)
		}
	}
	return err
}

// runInvestigationReconciler picks up investigations whose Redis job was never
// enqueued, was lost with a crashed worker, or is waiting out a retry backoff.
// It needs only Postgres, so it keeps working while Redis is down.
func (w *Worker) runInvestigationReconciler(ctx context.Context) {
	t := time.NewTicker(investigationReconcileEvery)
	defer t.Stop()
	for {
		w.reconcileInvestigations(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Worker) reconcileInvestigations(ctx context.Context) {
	due, err := w.investigationStore().ClaimDueInvestigations(ctx, investigationReconcileBatch, maxInvestigationAttempts, investigationLease)
	if err != nil {
		log.Printf("investigation reconcile: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, inv := range due {
		log.Printf("↻ recovering investigation for incident %s (attempt %d)", inv.IncidentID, inv.AttemptCount)
		wg.Add(1)
		go func(inv store.Investigation) {
			defer wg.Done()
			_ = w.runInvestigation(ctx, inv.TeamID, inv.IncidentID, inv.AttemptCount)
		}(inv)
	}
	wg.Wait()
}
