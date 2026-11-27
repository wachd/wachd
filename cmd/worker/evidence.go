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
	"github.com/wachd/wachd/internal/ai"
	"github.com/wachd/wachd/internal/correlator"
	"github.com/wachd/wachd/internal/store"
)

// evidenceSetFrom lists what was actually collected for an incident, so the AI
// answer can be checked against it.
func evidenceSetFrom(incident *store.Incident, c *correlator.Context) ai.EvidenceSet {
	var set ai.EvidenceSet
	if c == nil {
		return set
	}
	onset := incident.AnalysisTime()
	for _, commit := range c.Commits {
		ce := ai.CommitEvidence{SHA: commit.SHA, AfterOnset: !commit.Timestamp.IsZero() && commit.Timestamp.After(onset)}
		for _, ch := range commit.Changes {
			if ch.Patch != "" {
				ce.HasPatch = true
				break
			}
		}
		set.Commits = append(set.Commits, ce)
	}
	seen := map[string]bool{}
	for _, m := range c.Metrics {
		if m.Name != "" && !seen[m.Name] {
			seen[m.Name] = true
			set.Metrics = append(set.Metrics, m.Name)
		}
	}
	for _, l := range c.Logs {
		set.Logs = append(set.Logs, l.Message)
	}
	return set
}
