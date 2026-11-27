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
	"encoding/json"
	"strings"
	"time"
)

// minPlausibleOnset rejects zero and placeholder timestamps such as
// "0001-01-01T00:00:00Z" that Grafana uses for unset fields.
var minPlausibleOnset = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// AlertOnset returns when the source system says the alert started.
//
// Only Grafana unified-alerting payloads carry this today: the earliest valid
// alerts[].startsAt. A value is valid when it parses as RFC 3339, is a
// plausible date, and is not later than the time Wachd received the alert
// (FiredAt). Resolved alerts are ignored. Every other source, and any payload
// without a valid startsAt, reports ok=false.
func (i *Incident) AlertOnset() (time.Time, bool) {
	if i == nil || i.Source != "grafana" || len(i.AlertPayload) == 0 {
		return time.Time{}, false
	}
	var payload struct {
		Alerts []struct {
			Status   string `json:"status"`
			StartsAt string `json:"startsAt"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(i.AlertPayload, &payload); err != nil {
		return time.Time{}, false
	}
	var earliest time.Time
	for _, a := range payload.Alerts {
		if strings.EqualFold(strings.TrimSpace(a.Status), "resolved") {
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(a.StartsAt))
		if err != nil {
			continue
		}
		t = t.UTC()
		if t.Before(minPlausibleOnset) || t.After(i.FiredAt) {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest, !earliest.IsZero()
}

// AnalysisTime is the instant root-cause analysis is anchored to: the source
// alert onset when known, otherwise the receipt time. It must only be used for
// evidence collection and correlation. Lifecycle logic (escalation, ordering,
// notifications) keeps using FiredAt, which is always the receipt time.
func (i *Incident) AnalysisTime() time.Time {
	if onset, ok := i.AlertOnset(); ok {
		return onset
	}
	return i.FiredAt
}
