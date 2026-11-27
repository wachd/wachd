package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/oncall"
	"github.com/wachd/wachd/internal/sanitiser"
	"github.com/wachd/wachd/internal/store"
)

type scriptedAI struct {
	reply string
	err   error
}

func (s scriptedAI) Analyze(context.Context, string) (string, error) { return s.reply, s.err }
func (scriptedAI) GetModelName() string                              { return "scripted" }
func (scriptedAI) IsAvailable(context.Context) bool                  { return true }

func rcaFixture(t *testing.T, db *store.DB) (*store.Team, *store.Incident) {
	t.Helper()
	ctx := context.Background()
	team, err := db.CreateTeam(ctx, "rca-"+uuid.NewString()[:8], "sec-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })
	msg := "5xx"
	fp := uuid.NewString()
	inc := &store.Incident{TeamID: team.ID, Title: "checkout-api 5xx", Message: &msg, Severity: "high", Status: "open",
		Source: "grafana", Fingerprint: &fp,
		AlertPayload: []byte(`{"alerts":[{"status":"firing","startsAt":"2020-01-02T03:04:05Z","labels":{"service":"checkout-api"}}]}`)}
	if err := db.CreateIncidentWithNotificationPreparation(ctx, inc); err != nil {
		t.Fatal(err)
	}
	return team, inc
}

func rcaWorker(db *store.DB, backend ai.Backend) *Worker {
	return &Worker{db: db, oncallManager: oncall.NewManager(db), sanitiser: sanitiser.NewSanitiser(),
		correlator: correlator.NewCorrelator(), aiBackend: backend}
}

func storedAnalysis(t *testing.T, db *store.DB, team, inc uuid.UUID) map[string]any {
	t.Helper()
	got, err := db.GetIncident(context.Background(), team, inc)
	if err != nil || got == nil {
		t.Fatalf("GetIncident: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got.Analysis, &m); err != nil {
		t.Fatalf("analysis is not JSON: %v (%s)", err, got.Analysis)
	}
	return m
}

// The final RCA is merged into the preliminary timeline, keeping distinct
// fields, and unverifiable evidence is rejected instead of stored as fact.
func TestInvestigate_StoresRCAWithTimelineAndRejectsFabrication(t *testing.T) {
	db := requireWorkerDB(t)
	team, inc := rcaFixture(t, db)
	reply := `{"root_cause":"pool saturated, see commit deadbee","suggested_action":"raise the pool","key_findings":["pool at ceiling"],
	"confidence":"high","is_deployment_cause":true,"evidence_classification":"CONFIRMED_ROOT_CAUSE",
	"evidence":[{"type":"commit","ref":"deadbee"},{"type":"metric","ref":"db_pool_made_up"}]}`
	w := rcaWorker(db, scriptedAI{reply: reply})

	if err := w.investigateIncident(context.Background(), team.ID, inc.ID); err != nil {
		t.Fatalf("investigateIncident: %v", err)
	}
	a := storedAnalysis(t, db, team.ID, inc.ID)

	for _, k := range []string{"alert_time", "summary", "correlations"} {
		if _, ok := a[k]; !ok {
			t.Errorf("timeline key %q lost when the RCA was stored", k)
		}
	}
	if a["root_cause"] == "" || a["suggested_action"] != "raise the pool" {
		t.Errorf("root cause / action not stored distinctly: %v", a)
	}
	if a["evidence_classification"] != "INSUFFICIENT_EVIDENCE" || a["is_deployment_cause"] != false || a["confidence"] != "low" {
		t.Errorf("unsupported claims were not lowered: class=%v deploy=%v confidence=%v",
			a["evidence_classification"], a["is_deployment_cause"], a["confidence"])
	}
	rej, _ := a["rejected_evidence"].([]any)
	if len(rej) < 2 {
		t.Errorf("fabricated references not recorded: %v", a["rejected_evidence"])
	}
	findings, _ := a["key_findings"].([]any)
	var joined []string
	for _, f := range findings {
		joined = append(joined, f.(string))
	}
	if len(findings) < 2 || findings[0] != "pool at ceiling" || !strings.Contains(strings.Join(joined, " "), "discarded") {
		t.Errorf("findings should keep the model's and explain the rejection: %v", findings)
	}
}

// A malformed or failed AI answer is an error (retried, then a visible failed
// investigation), never a stored "successful" RCA.
func TestInvestigate_MalformedOrFailedAIIsNotPersistedAsRCA(t *testing.T) {
	db := requireWorkerDB(t)
	for name, backend := range map[string]scriptedAI{
		"prose":     {reply: "The database looks unhappy, maybe restart it."},
		"truncated": {reply: `{"root_cause":"pool saturated","suggested_action":"rai`},
		"no fields": {reply: `{"confidence":"high"}`},
		"provider":  {err: errors.New("claude: API returned 529")},
	} {
		team, inc := rcaFixture(t, db)
		err := rcaWorker(db, backend).investigateIncident(context.Background(), team.ID, inc.ID)
		if err == nil {
			t.Errorf("%s: want an error so the attempt is retried", name)
		}
		a := storedAnalysis(t, db, team.ID, inc.ID)
		if _, ok := a["root_cause"]; ok {
			t.Errorf("%s: a root cause was persisted: %v", name, a["root_cause"])
		}
		if _, ok := a["summary"]; !ok {
			t.Errorf("%s: preliminary timeline lost", name)
		}
	}
}

func TestEvidenceSetFrom_FlagsPostOnsetAndPatch(t *testing.T) {
	_, inc := rcaFixture(t, requireWorkerDB(t))
	onset, _ := inc.AlertOnset()
	// the fixture's own startsAt is far in the past, so use it as the onset
	c := &correlator.Context{}
	c.Commits = append(c.Commits,
		commitAt("aaaaaaa1", onset.Add(-time.Minute), "x"),
		commitAt("bbbbbbb2", onset.Add(time.Minute), ""))
	set := evidenceSetFrom(inc, c)
	if len(set.Commits) != 2 || set.Commits[0].AfterOnset || !set.Commits[0].HasPatch ||
		!set.Commits[1].AfterOnset || set.Commits[1].HasPatch {
		t.Fatalf("unexpected: %+v", set.Commits)
	}
}

func commitAt(sha string, ts time.Time, patch string) collector.Commit {
	c := collector.Commit{SHA: sha, Timestamp: ts}
	if patch != "" {
		c.Changes = []collector.FileChange{{Filename: "f", Patch: patch}}
	}
	return c
}

// No silent parsing failures: a model that keeps returning unusable output ends
// in a visible failed investigation with the reason, not a completed one.
func TestInvestigation_MalformedAIEndsVisiblyFailed(t *testing.T) {
	db := requireWorkerDB(t)
	team, inc := rcaFixture(t, db)
	w := rcaWorker(db, scriptedAI{reply: "I think it is the database."})
	ctx := context.Background()

	for attempt := 1; attempt <= maxInvestigationAttempts; attempt++ {
		if _, err := db.Pool().Exec(ctx, `UPDATE incident_investigations SET next_attempt_at = now() - interval '1 second' WHERE incident_id=$1`, inc.ID); err != nil {
			t.Fatal(err)
		}
		claimed, managed, got, err := db.ClaimInvestigation(ctx, inc.ID, investigationLease)
		if err != nil || !claimed || !managed || got != attempt {
			t.Fatalf("attempt %d: claimed=%v managed=%v got=%d err=%v", attempt, claimed, managed, got, err)
		}
		if err := w.runInvestigation(ctx, team.ID, inc.ID, got); err == nil {
			t.Fatalf("attempt %d: malformed answer reported as success", attempt)
		}
	}
	inv, err := db.GetInvestigation(ctx, team.ID, inc.ID)
	if err != nil || inv == nil {
		t.Fatal(err)
	}
	if inv.State != store.InvestigationFailed || inv.LastError == nil || !strings.Contains(*inv.LastError, "rejected") {
		t.Fatalf("want failed with a reason, got %+v", inv)
	}
	if a := storedAnalysis(t, db, team.ID, inc.ID); a["root_cause"] != nil {
		t.Errorf("an unusable answer was stored as the root cause: %v", a["root_cause"])
	}
}
