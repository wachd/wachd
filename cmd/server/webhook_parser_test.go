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
	"encoding/json"
	"testing"
)

func TestParseWebhookPayload_AgentKubescape(t *testing.T) {
	// wachd-agent payload must not be misclassified as grafana.
	// Before the fix: Grafana check fired on "title" alone → source=grafana, severity=unknown.
	payload := map[string]interface{}{
		"title":    "Kubescape: 3 vulnerability finding(s) in production/payments-api",
		"message":  "3 critical CVEs in runtime packages",
		"severity": "critical",
		"source":   "kubescape",
		"labels":   map[string]string{"env": "production"},
	}
	body, _ := json.Marshal(payload)

	title, message, severity, source := parseWebhookPayload(body)

	if source != "kubescape" {
		t.Errorf("source: want kubescape, got %q", source)
	}
	if severity != "critical" {
		t.Errorf("severity: want critical, got %q", severity)
	}
	if title != "Kubescape: 3 vulnerability finding(s) in production/payments-api" {
		t.Errorf("title mismatch: %q", title)
	}
	if message != "3 critical CVEs in runtime packages" {
		t.Errorf("message mismatch: %q", message)
	}
}

func TestParseWebhookPayload_AgentHighSeverity(t *testing.T) {
	payload := map[string]interface{}{
		"title":    "Kubescape: 2 misconfiguration finding(s) in default/api",
		"severity": "high",
		"source":   "kubescape",
	}
	body, _ := json.Marshal(payload)

	_, _, severity, source := parseWebhookPayload(body)

	if source != "kubescape" {
		t.Errorf("source: want kubescape, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high, got %q", severity)
	}
}

func TestParseWebhookPayload_GrafanaStillWorks(t *testing.T) {
	// Grafana payloads (no "source" field) must still be detected correctly.
	payload := map[string]interface{}{
		"title":   "CPU above threshold",
		"state":   "alerting",
		"message": "CPU is at 95%",
	}
	body, _ := json.Marshal(payload)

	_, _, severity, source := parseWebhookPayload(body)

	if source != "grafana" {
		t.Errorf("source: want grafana, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high for alerting state, got %q", severity)
	}
}

func TestParseWebhookPayload_DatadogStillWorks(t *testing.T) {
	payload := map[string]interface{}{
		"title":             "Service latency spike",
		"alert_type":        "error",
		"alert_transition":  "Triggered",
		"alert_id":          int64(12345),
	}
	body, _ := json.Marshal(payload)

	_, _, severity, source := parseWebhookPayload(body)

	if source != "datadog" {
		t.Errorf("source: want datadog, got %q", source)
	}
	if severity != "critical" {
		t.Errorf("severity: want critical for alert_type=error, got %q", severity)
	}
}

func TestParseWebhookPayload_GenericNoSource(t *testing.T) {
	// Payload with no "source", no "title" (avoids Grafana match), no Datadog signals.
	// Falls through to the generic fallback — uses "name" as title.
	payload := map[string]interface{}{
		"name":     "Something happened",
		"severity": "high",
	}
	body, _ := json.Marshal(payload)

	title, _, severity, source := parseWebhookPayload(body)

	if source != "generic" {
		t.Errorf("source: want generic, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high, got %q", severity)
	}
	if title != "Something happened" {
		t.Errorf("title: want 'Something happened', got %q", title)
	}
}

func TestParseWebhookPayload_EmptySourceFallsThrough(t *testing.T) {
	// An empty "source" field must not short-circuit — fall through to Grafana.
	payload := map[string]interface{}{
		"title":  "CPU alert",
		"source": "",
		"state":  "alerting",
	}
	body, _ := json.Marshal(payload)

	_, _, _, source := parseWebhookPayload(body)

	if source != "grafana" {
		t.Errorf("empty source should fall through to grafana detection, got %q", source)
	}
}
