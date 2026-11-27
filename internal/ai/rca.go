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

package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Evidence classes, from strongest to weakest claim.
const (
	ClassConfirmed    = "CONFIRMED_ROOT_CAUSE"
	ClassProbable     = "PROBABLE_ROOT_CAUSE"
	ClassTemporal     = "TEMPORAL_CORRELATION_ONLY"
	ClassInsufficient = "INSUFFICIENT_EVIDENCE"
)

// Parse modes recorded on the analysis.
const (
	ParseJSON     = "json"
	ParseMarkdown = "markdown"
)

const fallbackAction = "Review incident details and determine appropriate action"

// ErrMalformedRCA means the model answered but not with a usable analysis. The
// caller must treat the attempt as failed rather than store the text.
var ErrMalformedRCA = errors.New("malformed RCA response")

// EvidenceRef is one piece of evidence the model relied on. Verified is set by
// Wachd after checking the reference against what was actually collected; it is
// never taken from the model.
type EvidenceRef struct {
	Type     string `json:"type"` // commit | metric | log
	Ref      string `json:"ref"`
	Detail   string `json:"detail,omitempty"`
	Verified bool   `json:"verified"`
}

// rcaPayload is the JSON object the prompt asks the model to return.
type rcaPayload struct {
	RootCause         string        `json:"root_cause"`
	SuggestedAction   string        `json:"suggested_action"`
	KeyFindings       []string      `json:"key_findings"`
	Confidence        string        `json:"confidence"`
	EvidenceClass     string        `json:"evidence_classification"`
	IsDeploymentCause bool          `json:"is_deployment_cause"`
	CausalCommit      string        `json:"causal_commit"`
	Evidence          []EvidenceRef `json:"evidence"`
}

// ParseRCA turns a raw model answer into an AnalysisResponse. A JSON object is
// expected. Backends that cannot produce JSON (small local models) may answer
// in the labelled-section format; that is accepted only when it yields a real
// root cause and action. Anything else is ErrMalformedRCA.
func ParseRCA(raw string) (*AnalysisResponse, error) {
	if obj, ok := extractJSONObject(raw); ok {
		var p rcaPayload
		dec := json.NewDecoder(strings.NewReader(obj))
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%w: invalid JSON: %v", ErrMalformedRCA, err)
		}
		return fromPayload(&p)
	}
	r := ParseAnalysisResponse(raw)
	if strings.TrimSpace(r.RootCause) == "" || r.SuggestedAction == fallbackAction || strings.TrimSpace(r.SuggestedAction) == "" || r.RootCause == raw {
		return nil, fmt.Errorf("%w: no JSON object and no root cause/action sections", ErrMalformedRCA)
	}
	r.ParseMode = ParseMarkdown
	r.Evidence = nil
	return r, nil
}

func fromPayload(p *rcaPayload) (*AnalysisResponse, error) {
	var missing []string
	if strings.TrimSpace(p.RootCause) == "" {
		missing = append(missing, "root_cause")
	}
	if strings.TrimSpace(p.SuggestedAction) == "" {
		missing = append(missing, "suggested_action")
	}
	class := strings.ToUpper(strings.TrimSpace(p.EvidenceClass))
	switch class {
	case ClassConfirmed, ClassProbable, ClassTemporal, ClassInsufficient:
	default:
		missing = append(missing, "evidence_classification")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing or invalid %s", ErrMalformedRCA, strings.Join(missing, ", "))
	}
	conf := strings.ToLower(strings.TrimSpace(p.Confidence))
	if conf != "high" && conf != "medium" && conf != "low" {
		conf = "low" // unknown confidence is never presented as high
	}
	r := &AnalysisResponse{
		RootCause:         strings.TrimSpace(p.RootCause),
		SuggestedAction:   strings.TrimSpace(p.SuggestedAction),
		Confidence:        conf,
		KeyFindings:       []string{},
		IsDeploymentCause: p.IsDeploymentCause,
		CausalCommit:      strings.TrimSpace(p.CausalCommit),
		EvidenceClass:     class,
		ParseMode:         ParseJSON,
	}
	for _, f := range p.KeyFindings {
		if f = strings.TrimSpace(f); f != "" {
			r.KeyFindings = append(r.KeyFindings, f)
		}
	}
	for _, e := range p.Evidence {
		e.Type = strings.ToLower(strings.TrimSpace(e.Type))
		e.Ref = strings.TrimSpace(e.Ref)
		e.Verified = false // only Wachd decides this
		if e.Ref != "" {
			r.Evidence = append(r.Evidence, e)
		}
	}
	return r, nil
}

// extractJSONObject finds the outermost JSON object in text, tolerating code
// fences and prose around it.
func extractJSONObject(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return s[start:], true // unterminated (truncated): let the decoder report it
}

// CommitEvidence describes a commit that was actually collected.
type CommitEvidence struct {
	SHA        string
	HasPatch   bool // the collected commit includes a diff
	AfterOnset bool // committed after the alert began, so it cannot be the cause
}

// EvidenceSet is everything that was really collected for the incident.
type EvidenceSet struct {
	Commits []CommitEvidence
	Metrics []string // series names as collected
	Logs    []string // sanitised log messages
}

var (
	shaToken   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	identToken = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)
	promFuncs  = map[string]bool{"rate": true, "irate": true, "increase": true, "sum": true, "avg": true,
		"max": true, "min": true, "by": true, "histogram_quantile": true, "delta": true, "deriv": true}
)

// metricBase reduces "rate(db_pool_wait_count[5m])" and "db_pool_wait_count{service=..}"
// to the underlying metric name.
func metricBase(s string) string {
	for _, tok := range identToken.FindAllString(s, -1) {
		tok = strings.TrimRight(tok, ":") // "name: latest=5" is a label, not part of the name
		if tok != "" && !promFuncs[tok] {
			return tok
		}
	}
	return ""
}

func normText(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// VerifyAndCalibrate checks every evidence reference against what was
// collected and then limits the claims the evidence cannot support:
//
//   - references to commits, metrics or logs that were not collected are
//     rejected (kept only as a warning, never as evidence);
//   - commit ids written in the prose that were not collected are flagged;
//   - CONFIRMED_ROOT_CAUSE needs a verified, pre-onset commit that carries a
//     patch plus verified telemetry; PROBABLE_ROOT_CAUSE needs some verified
//     reference; otherwise the class is lowered;
//   - is_deployment_cause needs a verified pre-onset commit;
//   - confidence is capped by the final class.
//
// Every adjustment is recorded in Warnings and as a key finding.
func VerifyAndCalibrate(r *AnalysisResponse, set EvidenceSet) {
	if r == nil {
		return
	}
	commitByPrefix := func(ref string) (CommitEvidence, bool) {
		ref = strings.ToLower(strings.TrimSpace(ref))
		if len(ref) < 7 {
			return CommitEvidence{}, false
		}
		for _, c := range set.Commits {
			sha := strings.ToLower(c.SHA)
			if strings.HasPrefix(sha, ref) || strings.HasPrefix(ref, sha) && len(sha) >= 7 {
				return c, true
			}
		}
		return CommitEvidence{}, false
	}
	metricKnown := func(ref string) bool {
		b := metricBase(ref)
		if b == "" {
			return false
		}
		for _, m := range set.Metrics {
			if metricBase(m) == b {
				return true
			}
		}
		return false
	}
	logKnown := func(ref string) bool {
		n := normText(ref)
		if len(n) < 8 {
			return false
		}
		for _, l := range set.Logs {
			if strings.Contains(normText(l), n) {
				return true
			}
		}
		return false
	}

	var causalCommit, telemetry, anyVerified bool
	var evidenceCommit string // the one verified, usable commit ref, if unambiguous
	usableRefs := 0
	kept := r.Evidence[:0:0]
	for _, e := range r.Evidence {
		switch e.Type {
		case "commit":
			if c, ok := commitByPrefix(e.Ref); ok {
				e.Verified = true
				if !c.AfterOnset && c.HasPatch {
					usableRefs++
					evidenceCommit = e.Ref
				}
			}
		case "metric":
			e.Verified = metricKnown(e.Ref)
			telemetry = telemetry || e.Verified
		case "log":
			e.Verified = logKnown(e.Ref)
			telemetry = telemetry || e.Verified
		}
		if e.Verified {
			anyVerified = true
			kept = append(kept, e)
		} else {
			r.RejectedEvidence = append(r.RejectedEvidence, fmt.Sprintf("%s %q", e.Type, e.Ref))
		}
	}
	r.Evidence = kept

	// The commit the model holds responsible must itself be real, pre-onset and
	// carry a patch. Without an explicit one, a single usable cited commit is
	// taken only when the model also claimed a deployment cause.
	if cc := strings.TrimSpace(r.CausalCommit); cc != "" {
		c, ok := commitByPrefix(cc)
		switch {
		case !ok:
			r.RejectedEvidence = append(r.RejectedEvidence, fmt.Sprintf("causal commit %q", cc))
			r.CausalCommit = ""
		case c.AfterOnset:
			r.Warnings = append(r.Warnings, fmt.Sprintf("Causal commit %s was committed after the alert began and cannot be its cause.", cc))
			r.CausalCommit = ""
		case !c.HasPatch:
			r.Warnings = append(r.Warnings, fmt.Sprintf("Causal commit %s has no collected patch, so the change is unverified.", cc))
			r.CausalCommit = ""
		default:
			causalCommit = true
			r.CausalCommit = c.SHA
		}
	} else if r.IsDeploymentCause && usableRefs == 1 {
		if c, ok := commitByPrefix(evidenceCommit); ok {
			causalCommit = true
			r.CausalCommit = c.SHA
		}
	}

	// Commit ids in the prose must exist too.
	prose := r.RootCause + " " + r.SuggestedAction + " " + strings.Join(r.KeyFindings, " ")
	seen := map[string]bool{}
	for _, tok := range shaToken.FindAllString(strings.ToLower(prose), -1) {
		if !strings.ContainsAny(tok, "0123456789") || !strings.ContainsAny(tok, "abcdef") || seen[tok] {
			continue
		}
		seen[tok] = true
		if _, ok := commitByPrefix(tok); !ok {
			r.RejectedEvidence = append(r.RejectedEvidence, fmt.Sprintf("commit %q (named in text)", tok))
		}
	}

	var notes []string
	if len(r.RejectedEvidence) > 0 {
		notes = append(notes, fmt.Sprintf("Unverified evidence was discarded: %s.", strings.Join(r.RejectedEvidence, ", ")))
	}

	for _, w := range r.Warnings {
		notes = append(notes, w)
	}
	r.Warnings = nil
	if r.IsDeploymentCause && !causalCommit {
		r.IsDeploymentCause = false
		notes = append(notes, "A deployment cause was not accepted: no collected pre-onset commit with a patch supports it.")
	}

	orig := r.EvidenceClass
	switch {
	case r.EvidenceClass == ClassConfirmed && !(causalCommit && telemetry):
		r.EvidenceClass = ClassProbable
	}
	if (r.EvidenceClass == ClassProbable) && !anyVerified {
		r.EvidenceClass = ClassInsufficient
	}
	if len(r.RejectedEvidence) > 0 && r.EvidenceClass == ClassConfirmed {
		r.EvidenceClass = ClassProbable // fabricated references taint a confirmation
	}
	if r.EvidenceClass != orig {
		notes = append(notes, fmt.Sprintf("Classification lowered from %s to %s: the collected evidence does not support the stronger claim.", orig, r.EvidenceClass))
	}

	switch r.EvidenceClass {
	case ClassInsufficient:
		r.Confidence = "low"
	case ClassTemporal:
		if r.Confidence == "high" {
			r.Confidence = "medium"
		}
	}
	r.Warnings = append(r.Warnings, notes...)
	r.KeyFindings = append(r.KeyFindings, notes...)
}
