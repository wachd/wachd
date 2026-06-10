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

	"github.com/google/uuid"
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

	title, message, severity, source, resolved := parseWebhookPayload(body)

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
	if resolved {
		t.Error("resolved: want false for a firing alert")
	}
}

func TestParseWebhookPayload_AgentHighSeverity(t *testing.T) {
	payload := map[string]interface{}{
		"title":    "Kubescape: 2 misconfiguration finding(s) in default/api",
		"severity": "high",
		"source":   "kubescape",
	}
	body, _ := json.Marshal(payload)

	_, _, severity, source, resolved := parseWebhookPayload(body)

	if source != "kubescape" {
		t.Errorf("source: want kubescape, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high, got %q", severity)
	}
	if resolved {
		t.Error("resolved: want false for a firing alert")
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

	_, _, severity, source, resolved := parseWebhookPayload(body)

	if source != "grafana" {
		t.Errorf("source: want grafana, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high for alerting state, got %q", severity)
	}
	if resolved {
		t.Error("resolved: want false for alerting state")
	}
}

func TestParseWebhookPayload_DatadogStillWorks(t *testing.T) {
	payload := map[string]interface{}{
		"title":            "Service latency spike",
		"alert_type":       "error",
		"alert_transition": "Triggered",
		"alert_id":         int64(12345),
	}
	body, _ := json.Marshal(payload)

	_, _, severity, source, resolved := parseWebhookPayload(body)

	if source != "datadog" {
		t.Errorf("source: want datadog, got %q", source)
	}
	if severity != "critical" {
		t.Errorf("severity: want critical for alert_type=error, got %q", severity)
	}
	if resolved {
		t.Error("resolved: want false for alert_type=error")
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

	title, _, severity, source, resolved := parseWebhookPayload(body)

	if source != "generic" {
		t.Errorf("source: want generic, got %q", source)
	}
	if severity != "high" {
		t.Errorf("severity: want high, got %q", severity)
	}
	if title != "Something happened" {
		t.Errorf("title: want 'Something happened', got %q", title)
	}
	if resolved {
		t.Error("resolved: want false for generic payload")
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

	_, _, _, source, _ := parseWebhookPayload(body)

	if source != "grafana" {
		t.Errorf("empty source should fall through to grafana detection, got %q", source)
	}
}

func TestParseWebhookPayload_GrafanaResolvedReturnsTrue(t *testing.T) {
	payload := map[string]interface{}{
		"title": "CPU above threshold",
		"state": "ok",
	}
	body, _ := json.Marshal(payload)

	_, _, _, source, resolved := parseWebhookPayload(body)

	if source != "grafana" {
		t.Errorf("source: want grafana, got %q", source)
	}
	if !resolved {
		t.Error("resolved: want true for state=ok")
	}
}

func TestParseWebhookPayload_AgentResolvedReturnsTrue(t *testing.T) {
	payload := map[string]interface{}{
		"title":    "CrashLoopBackOff: payments-api/pod-xyz",
		"severity": "high",
		"source":   "k8shealth",
		"status":   "resolved",
	}
	body, _ := json.Marshal(payload)

	_, _, _, source, resolved := parseWebhookPayload(body)

	if source != "k8shealth" {
		t.Errorf("source: want k8shealth, got %q", source)
	}
	if !resolved {
		t.Error("resolved: want true for status=resolved")
	}
}

func TestParseWebhookPayload_DatadogResolvedReturnsTrue(t *testing.T) {
	payload := map[string]interface{}{
		"title":            "Service latency spike",
		"alert_type":       "success",
		"alert_transition": "Recovered",
		"alert_id":         int64(12345),
	}
	body, _ := json.Marshal(payload)

	_, _, _, source, resolved := parseWebhookPayload(body)

	if source != "datadog" {
		t.Errorf("source: want datadog, got %q", source)
	}
	if !resolved {
		t.Error("resolved: want true for alert_type=success")
	}
}

func TestIncidentFingerprint_Deterministic(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := incidentFingerprint(id, "grafana", "CPU above threshold")
	b := incidentFingerprint(id, "grafana", "CPU above threshold")

	if a != b {
		t.Errorf("fingerprint not deterministic: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("expected 64-char SHA-256 hex, got len %d: %q", len(a), a)
	}
}

func TestIncidentFingerprint_NormalizesTitle(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := incidentFingerprint(id, "grafana", "CPU Above Threshold")
	b := incidentFingerprint(id, "grafana", "  cpu above threshold  ")

	if a != b {
		t.Errorf("fingerprint should normalize title case and whitespace: %q != %q", a, b)
	}
}

func TestIncidentFingerprint_DifferentTeamsDiffer(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	a := incidentFingerprint(id1, "grafana", "CPU above threshold")
	b := incidentFingerprint(id2, "grafana", "CPU above threshold")

	if a == b {
		t.Error("fingerprints for different teams must differ")
	}
}

func TestIncidentFingerprint_DifferentSourcesDiffer(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	a := incidentFingerprint(id, "grafana", "CPU above threshold")
	b := incidentFingerprint(id, "datadog", "CPU above threshold")

	if a == b {
		t.Error("fingerprints for different sources must differ")
	}
}
