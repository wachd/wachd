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

package store

import (
	"os"
	"testing"
	"time"
)

var receipt = time.Date(2026, 11, 25, 13, 41, 58, 0, time.UTC)

func grafanaIncident(payload string) *Incident {
	return &Incident{Source: "grafana", AlertPayload: []byte(payload), FiredAt: receipt}
}

func TestAlertOnset_OldAlertUsesStartsAtNotReceiptTime(t *testing.T) {
	body, err := os.ReadFile("testdata/grafana_firing_checkout_api.json")
	if err != nil {
		t.Fatal(err)
	}
	inc := &Incident{Source: "grafana", AlertPayload: body, FiredAt: receipt}
	want := time.Date(2026, 11, 24, 12, 33, 30, 0, time.UTC)

	got, ok := inc.AlertOnset()
	if !ok || !got.Equal(want) {
		t.Fatalf("AlertOnset = %v, %v; want %v", got, ok, want)
	}
	if !inc.AnalysisTime().Equal(want) {
		t.Fatalf("AnalysisTime = %v, want onset %v", inc.AnalysisTime(), want)
	}
	if !inc.FiredAt.Equal(receipt) {
		t.Fatalf("FiredAt must stay the receipt time, got %v", inc.FiredAt)
	}
}

func TestAlertOnset_EarliestValidOfMultipleAlerts(t *testing.T) {
	inc := grafanaIncident(`{"alerts":[
		{"status":"firing","startsAt":"2026-11-24T14:00:00Z"},
		{"status":"firing","startsAt":"2026-11-24T12:33:30Z"},
		{"status":"firing","startsAt":"2026-11-24T13:00:00Z"}]}`)
	got, ok := inc.AlertOnset()
	if !ok || !got.Equal(time.Date(2026, 11, 24, 12, 33, 30, 0, time.UTC)) {
		t.Fatalf("expected earliest startsAt, got %v %v", got, ok)
	}
}

func TestAlertOnset_SkipsInvalidAndPicksEarliestValid(t *testing.T) {
	inc := grafanaIncident(`{"alerts":[
		{"status":"firing","startsAt":"not-a-time"},
		{"status":"firing","startsAt":"0001-01-01T00:00:00Z"},
		{"status":"firing","startsAt":""},
		{"status":"firing","startsAt":"2026-11-26T00:00:00Z"},
		{"status":"firing","startsAt":"2026-11-24T13:00:00Z"}]}`)
	got, ok := inc.AlertOnset()
	if !ok || !got.Equal(time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected the only valid startsAt, got %v %v", got, ok)
	}
}

func TestAlertOnset_FallsBackToReceiptWhenNoValidStartsAt(t *testing.T) {
	cases := map[string]string{
		"invalid":     `{"alerts":[{"status":"firing","startsAt":"yesterday"}]}`,
		"zero time":   `{"alerts":[{"status":"firing","startsAt":"0001-01-01T00:00:00Z"}]}`,
		"future":      `{"alerts":[{"status":"firing","startsAt":"2026-11-25T13:41:59Z"}]}`,
		"far future":  `{"alerts":[{"status":"firing","startsAt":"2099-01-01T00:00:00Z"}]}`,
		"no alerts":   `{"title":"x","state":"alerting"}`,
		"empty array": `{"alerts":[]}`,
		"not json":    `not json`,
		"resolved":    `{"alerts":[{"status":"resolved","startsAt":"2026-11-24T12:00:00Z"}]}`,
	}
	for name, payload := range cases {
		inc := grafanaIncident(payload)
		if _, ok := inc.AlertOnset(); ok {
			t.Errorf("%s: expected no onset", name)
		}
		if !inc.AnalysisTime().Equal(receipt) {
			t.Errorf("%s: AnalysisTime = %v, want receipt %v", name, inc.AnalysisTime(), receipt)
		}
	}
}

func TestAlertOnset_StartsAtEqualToReceiptIsAccepted(t *testing.T) {
	inc := grafanaIncident(`{"alerts":[{"status":"firing","startsAt":"2026-11-25T13:41:58Z"}]}`)
	if got, ok := inc.AlertOnset(); !ok || !got.Equal(receipt) {
		t.Fatalf("got %v %v", got, ok)
	}
}

func TestAlertOnset_OtherSourcesAreUnchanged(t *testing.T) {
	payload := `{"alerts":[{"status":"firing","startsAt":"2026-11-24T12:33:30Z"}]}`
	for _, src := range []string{"datadog", "generic", "prometheus", ""} {
		inc := &Incident{Source: src, AlertPayload: []byte(payload), FiredAt: receipt}
		if _, ok := inc.AlertOnset(); ok {
			t.Errorf("source %q must not use startsAt", src)
		}
		if !inc.AnalysisTime().Equal(receipt) {
			t.Errorf("source %q: AnalysisTime changed to %v", src, inc.AnalysisTime())
		}
	}
	var nilInc *Incident
	if _, ok := nilInc.AlertOnset(); ok {
		t.Error("nil incident must report no onset")
	}
}
