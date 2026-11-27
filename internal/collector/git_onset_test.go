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

package collector

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/wachd/wachd/internal/store"
)

// The real checkout-api timeline: a captured Grafana payload whose startsAt is
// 12:33:30 on 2026-11-24, received by Wachd the next day at 13:41:58.
func TestFetchCommitsForIncident_RealTimeline_OnlyPreOnsetCommitIsACandidate(t *testing.T) {
	payload, err := os.ReadFile("../store/testdata/grafana_firing_checkout_api.json")
	if err != nil {
		t.Fatal(err)
	}
	day := func(h, m, s int) time.Time { return time.Date(2026, 11, 24, h, m, s, 0, time.UTC) }
	incident := &store.Incident{
		Source:       "grafana",
		AlertPayload: payload,
		FiredAt:      time.Date(2026, 11, 25, 13, 41, 58, 0, time.UTC),
	}
	g := fakeGitHub(t, []map[string]any{
		commitAt("d2fd87ca", day(12, 49, 11)), // 50 -> 5, after onset
		commitAt("cb2b699e", day(12, 35, 46)), // 5 -> 50, after onset
		commitAt("da3db37f", day(12, 24, 9)),  // 50 -> 5, before onset
	})

	// Previous behaviour, anchored to receipt time: the post-onset commit led.
	old, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", incident.FiredAt, 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil || len(old) == 0 || old[0].SHA != "d2fd87ca" {
		t.Fatalf("expected the receipt-time anchor to surface d2fd87ca first (documents the bug), got %v %+v", err, old)
	}

	got, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", incident.AnalysisTime(), 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SHA != "da3db37f" {
		t.Fatalf("only da3db37f may be a candidate for the original onset, got %+v", got)
	}
	if got[0].Relation != RelationWithinWindow {
		t.Errorf("da3db37f relation = %q, want %q", got[0].Relation, RelationWithinWindow)
	}
	for _, c := range got {
		if c.SHA == "cb2b699e" || c.SHA == "d2fd87ca" {
			t.Errorf("%s was made after the alert onset and must not be a candidate", c.SHA)
		}
	}
}
