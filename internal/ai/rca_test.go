package ai

import (
	"errors"
	"strings"
	"testing"
)

const validJSON = `{"root_cause":"Pool capped at 5 after da3db37 changed DB_POOL_MAX 50 -> 5.",
"suggested_action":"Revert da3db37 and redeploy.","key_findings":["db_pool_in_use max=5"],
"confidence":"high","is_deployment_cause":true,"evidence_classification":"CONFIRMED_ROOT_CAUSE",
"evidence":[{"type":"commit","ref":"da3db37","detail":"DB_POOL_MAX 50->5"},
{"type":"metric","ref":"db_pool_in_use","detail":"max 5"},
{"type":"log","ref":"db_connection_timeout","detail":""}]}`

func lab() EvidenceSet {
	return EvidenceSet{
		Commits: []CommitEvidence{{SHA: "da3db37aaaaaaaa", HasPatch: true}, {SHA: "2a74f4c1111111", HasPatch: true},
			{SHA: "9a914b2bbbbbbbb", HasPatch: true, AfterOnset: true}},
		Metrics: []string{"db_pool_in_use", "rate(db_pool_wait_count[5m])", "rate(http_errors_total[30m0s])"},
		Logs:    []string{`level=error msg="db_connection_timeout: context deadline exceeded" service=checkout-api`},
	}
}

func mustParse(t *testing.T, raw string) *AnalysisResponse {
	t.Helper()
	r, err := ParseRCA(raw)
	if err != nil {
		t.Fatalf("ParseRCA: %v", err)
	}
	return r
}

// ── schema and parsing ────────────────────────────────────────────────────────

func TestParseRCA_ValidJSON(t *testing.T) {
	r := mustParse(t, validJSON)
	if r.ParseMode != ParseJSON || r.EvidenceClass != ClassConfirmed || r.Confidence != "high" || !r.IsDeploymentCause {
		t.Fatalf("unexpected: %+v", r)
	}
	if r.RootCause == "" || r.SuggestedAction == "" || len(r.KeyFindings) != 1 || len(r.Evidence) != 3 {
		t.Fatalf("fields not kept distinct: %+v", r)
	}
}

func TestParseRCA_ToleratesFencesAndProse(t *testing.T) {
	for _, raw := range []string{"```json\n" + validJSON + "\n```", "Here is the analysis:\n" + validJSON + "\nHope that helps."} {
		if r := mustParse(t, raw); r.RootCause == "" {
			t.Errorf("not parsed: %q", raw[:20])
		}
	}
}

func TestParseRCA_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"prose only":       "The database pool is saturated, increase it.",
		"truncated json":   validJSON[:len(validJSON)-60],
		"missing root":     `{"suggested_action":"x","evidence_classification":"PROBABLE_ROOT_CAUSE"}`,
		"missing action":   `{"root_cause":"x","evidence_classification":"PROBABLE_ROOT_CAUSE"}`,
		"missing class":    `{"root_cause":"x","suggested_action":"y"}`,
		"invalid class":    `{"root_cause":"x","suggested_action":"y","evidence_classification":"DEFINITELY"}`,
		"blank root cause": `{"root_cause":"   ","suggested_action":"y","evidence_classification":"PROBABLE_ROOT_CAUSE"}`,
		"wrong types":      `{"root_cause":["a"],"suggested_action":"y","evidence_classification":"PROBABLE_ROOT_CAUSE"}`,
	}
	for name, raw := range cases {
		if _, err := ParseRCA(raw); !errors.Is(err, ErrMalformedRCA) {
			t.Errorf("%s: want ErrMalformedRCA, got %v", name, err)
		}
	}
}

// Partial answers are errors, never a "successful" RCA that dumps the raw text.
func TestParseRCA_NeverFallsBackToRawText(t *testing.T) {
	raw := "## Incident Analysis\nsomething vague with no sections at all"
	if r, err := ParseRCA(raw); err == nil {
		t.Fatalf("accepted unusable response: %+v", r)
	}
}

func TestParseRCA_UnknownConfidenceIsLow(t *testing.T) {
	r := mustParse(t, `{"root_cause":"x","suggested_action":"y","confidence":"certain","evidence_classification":"INSUFFICIENT_EVIDENCE"}`)
	if r.Confidence != "low" {
		t.Errorf("confidence = %q", r.Confidence)
	}
}

func TestParseRCA_MarkdownFallbackNeedsRealSections(t *testing.T) {
	r := mustParse(t, "### 1. ROOT CAUSE\nPool exhausted.\n### 2. SUGGESTED ACTION\nRaise the pool.\n### 5. EVIDENCE CLASSIFICATION\nPROBABLE_ROOT_CAUSE\n")
	if r.ParseMode != ParseMarkdown || r.RootCause != "Pool exhausted." || r.SuggestedAction != "Raise the pool." {
		t.Fatalf("unexpected: %+v", r)
	}
	if _, err := ParseRCA("### 1. ROOT CAUSE\nPool exhausted.\n"); err == nil {
		t.Error("markdown without a suggested action must be rejected")
	}
}

// The model must not be able to mark its own evidence as verified.
func TestParseRCA_IgnoresModelSuppliedVerified(t *testing.T) {
	r := mustParse(t, `{"root_cause":"x","suggested_action":"y","evidence_classification":"PROBABLE_ROOT_CAUSE",
	"evidence":[{"type":"metric","ref":"made_up","verified":true}]}`)
	if r.Evidence[0].Verified {
		t.Fatal("verified flag accepted from the model")
	}
}

// ── evidence verification and calibration ─────────────────────────────────────

func TestVerify_ValidConfirmedIsKept(t *testing.T) {
	r := mustParse(t, validJSON)
	VerifyAndCalibrate(r, lab())
	if r.EvidenceClass != ClassConfirmed || !r.IsDeploymentCause || len(r.RejectedEvidence) != 0 || len(r.Evidence) != 3 {
		t.Fatalf("valid RCA was altered: %+v", r)
	}
	for _, e := range r.Evidence {
		if !e.Verified {
			t.Errorf("%s %s not verified", e.Type, e.Ref)
		}
	}
}

// Seen in a real run: the model cited a metric that was never collected.
func TestVerify_FabricatedMetricRejected(t *testing.T) {
	r := mustParse(t, `{"root_cause":"pool saturated","suggested_action":"raise pool","confidence":"high",
	"evidence_classification":"PROBABLE_ROOT_CAUSE",
	"evidence":[{"type":"metric","ref":"db_pool_max_open_connections"}]}`)
	VerifyAndCalibrate(r, lab())
	if len(r.Evidence) != 0 || len(r.RejectedEvidence) != 1 {
		t.Fatalf("fabricated metric accepted: %+v", r)
	}
	if r.EvidenceClass != ClassInsufficient || r.Confidence != "low" {
		t.Errorf("unsupported claim not lowered: class=%s confidence=%s", r.EvidenceClass, r.Confidence)
	}
	if !strings.Contains(strings.Join(r.KeyFindings, " "), "db_pool_max_open_connections") {
		t.Error("rejection not surfaced in key findings")
	}
}

func TestVerify_NonexistentCommitRejectedAndNeverConfirmed(t *testing.T) {
	r := mustParse(t, `{"root_cause":"caused by deadbee","suggested_action":"revert deadbee","confidence":"high",
	"is_deployment_cause":true,"evidence_classification":"CONFIRMED_ROOT_CAUSE",
	"evidence":[{"type":"commit","ref":"deadbee"},{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if r.IsDeploymentCause {
		t.Error("deployment cause accepted without a real commit")
	}
	if r.EvidenceClass == ClassConfirmed {
		t.Error("CONFIRMED accepted with a fabricated commit")
	}
	if len(r.RejectedEvidence) == 0 {
		t.Error("fabricated commit not recorded")
	}
}

func TestVerify_ConfirmedWithoutCommitIsLowered(t *testing.T) {
	r := mustParse(t, `{"root_cause":"pool saturated","suggested_action":"raise pool","confidence":"high",
	"evidence_classification":"CONFIRMED_ROOT_CAUSE","evidence":[{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if r.EvidenceClass != ClassProbable {
		t.Fatalf("class = %s, want PROBABLE_ROOT_CAUSE", r.EvidenceClass)
	}
}

func TestVerify_ConfirmedWithCommitButNoPatchIsLowered(t *testing.T) {
	set := lab()
	set.Commits = []CommitEvidence{{SHA: "da3db37aaaaaaaa", HasPatch: false}}
	r := mustParse(t, validJSON)
	VerifyAndCalibrate(r, set)
	if r.EvidenceClass == ClassConfirmed || r.IsDeploymentCause {
		t.Fatalf("confirmed without a patch: class=%s deploy=%v", r.EvidenceClass, r.IsDeploymentCause)
	}
}

// S3: a commit made after the onset can never be the cause.
func TestVerify_PostOnsetCommitIsNotACause(t *testing.T) {
	r := mustParse(t, `{"root_cause":"9a914b2 broke it","suggested_action":"revert","confidence":"high",
	"is_deployment_cause":true,"evidence_classification":"CONFIRMED_ROOT_CAUSE",
	"evidence":[{"type":"commit","ref":"9a914b2"},{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if r.IsDeploymentCause || r.EvidenceClass == ClassConfirmed {
		t.Fatalf("post-onset commit accepted as cause: %+v", r)
	}
}

func TestVerify_NoEvidenceMeansInsufficientAndLowConfidence(t *testing.T) {
	r := mustParse(t, `{"root_cause":"probably the pool","suggested_action":"look","confidence":"high","evidence_classification":"PROBABLE_ROOT_CAUSE"}`)
	VerifyAndCalibrate(r, lab())
	if r.EvidenceClass != ClassInsufficient || r.Confidence != "low" {
		t.Fatalf("class=%s confidence=%s", r.EvidenceClass, r.Confidence)
	}
}

func TestVerify_TemporalConfidenceCapped(t *testing.T) {
	r := mustParse(t, `{"root_cause":"x","suggested_action":"y","confidence":"high","evidence_classification":"TEMPORAL_CORRELATION_ONLY",
	"evidence":[{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if r.Confidence != "medium" || r.EvidenceClass != ClassTemporal {
		t.Fatalf("class=%s confidence=%s", r.EvidenceClass, r.Confidence)
	}
}

// Commit ids in prose that were never collected are flagged even without a ref.
func TestVerify_UnknownShaInProseFlagged(t *testing.T) {
	r := mustParse(t, `{"root_cause":"commit 1234abc changed it","suggested_action":"y","confidence":"medium","evidence_classification":"PROBABLE_ROOT_CAUSE",
	"evidence":[{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if len(r.RejectedEvidence) != 1 || !strings.Contains(r.RejectedEvidence[0], "1234abc") {
		t.Fatalf("unknown sha not flagged: %v", r.RejectedEvidence)
	}
}

func TestVerify_RealShaInProseAndPlainNumbersAreFine(t *testing.T) {
	r := mustParse(t, `{"root_cause":"da3db37 lowered it; 1234567 requests failed; decade","suggested_action":"y","confidence":"medium",
	"evidence_classification":"PROBABLE_ROOT_CAUSE","evidence":[{"type":"metric","ref":"db_pool_in_use"}]}`)
	VerifyAndCalibrate(r, lab())
	if len(r.RejectedEvidence) != 0 {
		t.Fatalf("false positive: %v", r.RejectedEvidence)
	}
}

func TestVerify_MetricNamesMatchByBaseName(t *testing.T) {
	for ref, want := range map[string]bool{
		"db_pool_wait_count": true, "rate(db_pool_wait_count[5m])": true, `db_pool_in_use{service="x"}`: true,
		"http_errors_total": true, "db_pool_wait": false, "": false,
	} {
		r := &AnalysisResponse{RootCause: "x", SuggestedAction: "y", EvidenceClass: ClassProbable, Confidence: "medium",
			Evidence: []EvidenceRef{{Type: "metric", Ref: ref}}}
		VerifyAndCalibrate(r, lab())
		if got := len(r.Evidence) == 1; got != want {
			t.Errorf("metric %q verified=%v want %v", ref, got, want)
		}
	}
}

func TestVerify_LogMustBeInCollectedLogs(t *testing.T) {
	good := &AnalysisResponse{RootCause: "x", SuggestedAction: "y", EvidenceClass: ClassProbable, Confidence: "medium",
		Evidence: []EvidenceRef{{Type: "log", Ref: "db_connection_timeout: context deadline exceeded"}}}
	VerifyAndCalibrate(good, lab())
	if len(good.Evidence) != 1 {
		t.Error("real log fragment rejected")
	}
	bad := &AnalysisResponse{RootCause: "x", SuggestedAction: "y", EvidenceClass: ClassProbable, Confidence: "medium",
		Evidence: []EvidenceRef{{Type: "log", Ref: "OutOfMemoryError: heap space"}}}
	VerifyAndCalibrate(bad, lab())
	if len(bad.Evidence) != 0 || len(bad.RejectedEvidence) != 1 {
		t.Error("fabricated log accepted")
	}
}

func TestVerify_UnknownEvidenceTypeRejected(t *testing.T) {
	r := &AnalysisResponse{RootCause: "x", SuggestedAction: "y", EvidenceClass: ClassProbable, Confidence: "medium",
		Evidence: []EvidenceRef{{Type: "trace", Ref: "abc123456789"}}}
	VerifyAndCalibrate(r, lab())
	if len(r.Evidence) != 0 {
		t.Error("unknown evidence type accepted")
	}
}

func TestVerify_NilSafe(t *testing.T) { VerifyAndCalibrate(nil, lab()) }
