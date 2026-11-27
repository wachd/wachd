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
	"strings"
	"testing"
	"time"

	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/store"
)

var (
	onsetReceipt = time.Date(2026, 11, 25, 13, 41, 58, 0, time.UTC)
	onsetStart   = time.Date(2026, 11, 24, 12, 33, 30, 0, time.UTC)
)

func historicalGrafanaIncident() *store.Incident {
	return &store.Incident{
		Source:       "grafana",
		FiredAt:      onsetReceipt,
		AlertPayload: []byte(`{"alerts":[{"status":"firing","startsAt":"2026-11-24T12:33:30Z"}]}`),
	}
}

// Escalation and notification timing run from receipt time. An alert that
// started a day before Wachd saw it must not make every step overdue.
func TestEscalationStepDue_UsesReceiptTimeNotAlertOnset(t *testing.T) {
	inc := historicalGrafanaIncident()
	if !inc.AnalysisTime().Equal(onsetStart) {
		t.Fatalf("precondition: analysis time should be the onset, got %v", inc.AnalysisTime())
	}

	now := onsetReceipt.Add(2 * time.Minute)
	if escalationStepDue(inc, 10, now) {
		t.Error("a 10-minute step must not be due 2 minutes after receipt, even though the alert started a day earlier")
	}
	if escalationStepDue(inc, 0+1, onsetReceipt.Add(30*time.Second)) {
		t.Error("the minimum 1-minute step must not be due 30 seconds after receipt")
	}
	if !escalationStepDue(inc, 10, onsetReceipt.Add(10*time.Minute)) {
		t.Error("a 10-minute step must be due 10 minutes after receipt")
	}
	if !escalationStepDue(inc, 30, onsetReceipt.Add(31*time.Minute)) {
		t.Error("a 30-minute step must be due 31 minutes after receipt")
	}
}

func TestEscalationStepDue_NonGrafanaIncidentUnchanged(t *testing.T) {
	inc := &store.Incident{Source: "generic", FiredAt: onsetReceipt, AlertPayload: []byte(`{"alerts":[{"startsAt":"2026-11-24T12:33:30Z"}]}`)}
	if escalationStepDue(inc, 10, onsetReceipt.Add(9*time.Minute)) || !escalationStepDue(inc, 10, onsetReceipt.Add(10*time.Minute)) {
		t.Error("generic incident escalation timing changed")
	}
}

func TestFormatCommitForPrompt_CommitAfterOnsetIsNotPresentedAsBeforeIt(t *testing.T) {
	c := collector.Commit{Message: "config: adjust service capacity", Author: "dev", Timestamp: onsetStart.Add(15*time.Minute + 41*time.Second)}
	got := formatCommitForPrompt(c, "d2fd87c", onsetStart)
	if strings.Contains(got, "before the alert") {
		t.Errorf("a later commit was described as before the alert: %s", got)
	}
	if !strings.Contains(got, "AFTER the alert started") || !strings.Contains(got, "cannot have caused the original onset") {
		t.Errorf("missing after-onset wording: %s", got)
	}
}

func TestFormatCommitForPrompt_PreOnsetCommitStillBeforeAlert(t *testing.T) {
	c := collector.Commit{Message: "config: adjust service capacity", Author: "dev", Timestamp: onsetStart.Add(-9*time.Minute - 21*time.Second), Relation: collector.RelationWithinWindow}
	got := formatCommitForPrompt(c, "da3db37", onsetStart)
	if !strings.Contains(got, "9m0s before the alert") && !strings.Contains(got, "9m21s before the alert") {
		t.Errorf("expected pre-onset age, got %s", got)
	}
}

func TestOnsetTimelineNote(t *testing.T) {
	note := onsetTimelineNote(historicalGrafanaIncident())
	for _, want := range []string{"2026-11-24T12:33:30Z", "2026-11-25T13:41:58Z", "not proof that it was deployed"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q: %s", want, note)
		}
	}
	// No source onset, or onset equal to receipt: nothing to say.
	if got := onsetTimelineNote(&store.Incident{Source: "datadog", FiredAt: onsetReceipt}); got != "" {
		t.Errorf("non-grafana incident got a note: %q", got)
	}
	same := &store.Incident{Source: "grafana", FiredAt: onsetReceipt, AlertPayload: []byte(`{"alerts":[{"status":"firing","startsAt":"2026-11-25T13:41:58Z"}]}`)}
	if got := onsetTimelineNote(same); got != "" {
		t.Errorf("onset equal to receipt got a note: %q", got)
	}
}

func TestLimitEvidenceForOnset_NoTelemetryIsInsufficient(t *testing.T) {
	a := &ai.AnalysisResponse{EvidenceClass: "PROBABLE_ROOT_CAUSE", IsDeploymentCause: true}
	limitEvidenceForOnset(historicalGrafanaIncident(), &correlator.Context{Commits: []collector.Commit{{SHA: "da3db37"}}}, a)
	if a.EvidenceClass != "INSUFFICIENT_EVIDENCE" {
		t.Errorf("class = %q, want INSUFFICIENT_EVIDENCE when no logs or metrics cover the onset", a.EvidenceClass)
	}
}

func TestLimitEvidenceForOnset_ConfirmedDeploymentCappedToProbable(t *testing.T) {
	a := &ai.AnalysisResponse{EvidenceClass: "CONFIRMED_ROOT_CAUSE", IsDeploymentCause: true}
	ctx := &correlator.Context{Metrics: []collector.MetricPoint{{Name: "db_pool_in_use", Value: 5, Timestamp: onsetStart}}}
	limitEvidenceForOnset(historicalGrafanaIncident(), ctx, a)
	if a.EvidenceClass != "PROBABLE_ROOT_CAUSE" {
		t.Errorf("class = %q, want PROBABLE_ROOT_CAUSE: a commit is not deployment evidence", a.EvidenceClass)
	}
}

func TestLimitEvidenceForOnset_LeavesOtherIncidentsAlone(t *testing.T) {
	for _, inc := range []*store.Incident{
		{Source: "datadog", FiredAt: onsetReceipt},
		{Source: "grafana", FiredAt: onsetReceipt, AlertPayload: []byte(`{"alerts":[{"startsAt":"garbage"}]}`)},
	} {
		a := &ai.AnalysisResponse{EvidenceClass: "CONFIRMED_ROOT_CAUSE", IsDeploymentCause: true}
		limitEvidenceForOnset(inc, &correlator.Context{}, a)
		if a.EvidenceClass != "CONFIRMED_ROOT_CAUSE" {
			t.Errorf("%s incident without an onset was changed to %q", inc.Source, a.EvidenceClass)
		}
	}
	limitEvidenceForOnset(historicalGrafanaIncident(), nil, nil) // must not panic
}
