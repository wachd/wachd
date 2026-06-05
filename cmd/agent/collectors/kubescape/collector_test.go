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

package kubescape

import (
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// vulnSummary builds a fake VulnerabilityManifestSummary unstructured object.
// critRelevant / highRelevant are the .relevant severity counts.
// allCounts are set to 2× relevant to verify the collector ignores .all.
func vulnSummary(name, status string, gen int64, critRelevant, highRelevant int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":       name,
				"namespace":  "kubescape",
				"generation": gen,
				"annotations": map[string]interface{}{
					annotationStatus: status,
				},
				"labels": map[string]interface{}{
					labelNamespace: "production",
					labelKind:      "Deployment",
					labelName:      "payments-api",
				},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{
						"all":      critRelevant * 2,
						"relevant": critRelevant,
					},
					"high": map[string]interface{}{
						"all":      highRelevant * 2,
						"relevant": highRelevant,
					},
				},
			},
		},
	}
}

// configScanSummary builds a fake WorkloadConfigurationScanSummary.
// Severities are direct integers (no all/relevant split).
func configScanSummary(name, status string, gen int64, critical, high int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":       name,
				"namespace":  "kubescape",
				"generation": gen,
				"annotations": map[string]interface{}{
					annotationStatus: status,
				},
				"labels": map[string]interface{}{
					labelNamespace: "production",
					labelKind:      "Deployment",
					labelName:      "payments-api",
				},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": critical,
					"high":     high,
				},
			},
		},
	}
}

// newTestCollector builds a Collector with no k8s client — safe for reconcile tests.
func newTestCollector(minSeverity string) *Collector {
	return &Collector{
		minSeverity: minSeverity,
		state:       newState(),
	}
}

// ── State tests ───────────────────────────────────────────────────────────────

func TestStateIsNew_FirstCallReturnsTrue(t *testing.T) {
	s := newState()
	if !s.isNew("key", 3) {
		t.Fatal("first call with value > 0 should return true")
	}
}

func TestStateIsNew_SameValueReturnsFalse(t *testing.T) {
	s := newState()
	s.isNew("key", 3)
	if s.isNew("key", 3) {
		t.Fatal("second call with same value should return false")
	}
}

func TestStateIsNew_LowerValueReturnsFalse(t *testing.T) {
	s := newState()
	s.isNew("key", 5)
	if s.isNew("key", 3) {
		t.Fatal("lower value (findings resolved) should return false")
	}
}

func TestStateIsNew_HigherValueReturnsTrue(t *testing.T) {
	s := newState()
	s.isNew("key", 3)
	if !s.isNew("key", 5) {
		t.Fatal("higher value (new findings) should return true")
	}
}

func TestStateIsNew_ZeroValueReturnsFalse(t *testing.T) {
	s := newState()
	if s.isNew("key", 0) {
		t.Fatal("zero value should return false (0 is not > 0)")
	}
}

func TestStateIsNew_IndependentKeys(t *testing.T) {
	s := newState()
	s.isNew("a", 5)
	if !s.isNew("b", 1) {
		t.Fatal("different keys are tracked independently")
	}
}

func TestStateIsNew_ConcurrentAccess(t *testing.T) {
	s := newState()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.isNew("key", n)
		}(i)
	}
	wg.Wait()
	// Just verifying no race condition — no assertion on final value needed.
}

// ── nestedInt tests ───────────────────────────────────────────────────────────

func TestNestedInt_DirectInt64(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": int64(4),
				},
			},
		},
	}
	if got := nestedInt(obj, "spec", "severities", "critical"); got != 4 {
		t.Fatalf("want 4, got %d", got)
	}
}

func TestNestedInt_Float64FromJSON(t *testing.T) {
	// JSON decoder produces float64 for numbers — must be handled.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": float64(4),
				},
			},
		},
	}
	if got := nestedInt(obj, "spec", "severities", "critical"); got != 4 {
		t.Fatalf("want 4, got %d (float64 from JSON decoder must be handled)", got)
	}
}

func TestNestedInt_NestedPath(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{
						"all":      int64(10),
						"relevant": int64(3),
					},
				},
			},
		},
	}
	if got := nestedInt(obj, "spec", "severities", "critical", "relevant"); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
}

func TestNestedInt_MissingPath(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{}}
	if got := nestedInt(obj, "spec", "severities", "critical"); got != 0 {
		t.Fatalf("missing path should return 0, got %d", got)
	}
}

// ── reconcile tests ───────────────────────────────────────────────────────────

func TestReconcile_SkipsIncompleteScans(t *testing.T) {
	c := newTestCollector("high")
	obj := vulnSummary("scan-1", "", 1, 2, 3) // status is empty, not "completed"
	_, fire := c.reconcile(obj, "vulnerability")
	if fire {
		t.Fatal("should not fire when kubescape.io/status != completed")
	}
}

func TestReconcile_VulnCriticalFires(t *testing.T) {
	c := newTestCollector("high")
	obj := vulnSummary("scan-1", statusCompleted, 1, 2, 0)
	ev, fire := c.reconcile(obj, "vulnerability")
	if !fire {
		t.Fatal("expected event for critical vuln findings")
	}
	if ev.Severity != "critical" {
		t.Fatalf("want severity critical, got %s", ev.Severity)
	}
	if ev.Source != "kubescape" {
		t.Fatalf("want source kubescape, got %s", ev.Source)
	}
}

func TestReconcile_VulnHighFires(t *testing.T) {
	c := newTestCollector("high")
	obj := vulnSummary("scan-1", statusCompleted, 1, 0, 3)
	ev, fire := c.reconcile(obj, "vulnerability")
	if !fire {
		t.Fatal("expected event for high vuln findings")
	}
	if ev.Severity != "high" {
		t.Fatalf("want severity high, got %s", ev.Severity)
	}
}

func TestReconcile_VulnUsesRelevantNotAll(t *testing.T) {
	c := newTestCollector("high")
	// .all is non-zero but .relevant is 0 — should not fire.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "scan-1", "namespace": "kubescape", "generation": int64(1),
				"annotations": map[string]interface{}{annotationStatus: statusCompleted},
				"labels":      map[string]interface{}{labelNamespace: "prod", labelKind: "Deployment", labelName: "api"},
			},
			"spec": map[string]interface{}{
				"severities": map[string]interface{}{
					"critical": map[string]interface{}{"all": int64(5), "relevant": int64(0)},
					"high":     map[string]interface{}{"all": int64(8), "relevant": int64(0)},
				},
			},
		},
	}
	_, fire := c.reconcile(obj, "vulnerability")
	if fire {
		t.Fatal("should not fire when .relevant is 0, even if .all > 0")
	}
}

func TestReconcile_ConfigCriticalFires(t *testing.T) {
	c := newTestCollector("high")
	obj := configScanSummary("scan-1", statusCompleted, 1, 2, 0)
	ev, fire := c.reconcile(obj, "misconfiguration")
	if !fire {
		t.Fatal("expected event for critical config findings")
	}
	if ev.Severity != "critical" {
		t.Fatalf("want critical, got %s", ev.Severity)
	}
}

func TestReconcile_ConfigHighFires(t *testing.T) {
	c := newTestCollector("high")
	obj := configScanSummary("scan-1", statusCompleted, 1, 0, 3)
	ev, fire := c.reconcile(obj, "misconfiguration")
	if !fire {
		t.Fatal("expected event for high config findings")
	}
	if ev.Severity != "high" {
		t.Fatalf("want high, got %s", ev.Severity)
	}
}

func TestReconcile_MinSeverityCritical_HighIgnored(t *testing.T) {
	c := newTestCollector("critical")
	obj := vulnSummary("scan-1", statusCompleted, 1, 0, 5) // only high, no critical
	_, fire := c.reconcile(obj, "vulnerability")
	if fire {
		t.Fatal("should not fire when minSeverity=critical and only high findings present")
	}
}

func TestReconcile_MinSeverityCritical_CriticalFires(t *testing.T) {
	c := newTestCollector("critical")
	obj := vulnSummary("scan-1", statusCompleted, 1, 2, 5)
	ev, fire := c.reconcile(obj, "vulnerability")
	if !fire {
		t.Fatal("expected event when minSeverity=critical and critical findings present")
	}
	if ev.Severity != "critical" {
		t.Fatalf("want critical, got %s", ev.Severity)
	}
}

func TestReconcile_SkipsOnUnchangedGeneration(t *testing.T) {
	c := newTestCollector("high")
	obj := vulnSummary("scan-1", statusCompleted, 1, 2, 0)
	c.reconcile(obj, "vulnerability") // first call — fires and stores gen=1

	// Second call with same object (same generation) — should skip.
	_, fire := c.reconcile(obj, "vulnerability")
	if fire {
		t.Fatal("should not fire when generation is unchanged (no-op reconcile)")
	}
}

func TestReconcile_FiresOnNewGenerationWithHigherCount(t *testing.T) {
	c := newTestCollector("high")
	obj1 := vulnSummary("scan-1", statusCompleted, 1, 2, 0)
	c.reconcile(obj1, "vulnerability")

	// Same workload, new generation, more findings.
	obj2 := vulnSummary("scan-1", statusCompleted, 2, 4, 0)
	_, fire := c.reconcile(obj2, "vulnerability")
	if !fire {
		t.Fatal("expected event when generation bumped and finding count increased")
	}
}

func TestReconcile_NoFireOnNewGenerationSameCount(t *testing.T) {
	c := newTestCollector("high")
	obj1 := vulnSummary("scan-1", statusCompleted, 1, 2, 0)
	c.reconcile(obj1, "vulnerability")

	// New generation but same finding count — re-scan with no new findings.
	obj2 := vulnSummary("scan-1", statusCompleted, 2, 2, 0)
	_, fire := c.reconcile(obj2, "vulnerability")
	if fire {
		t.Fatal("should not fire when generation bumped but finding count is unchanged")
	}
}

func TestReconcile_NoFireWhenFindingsResolved(t *testing.T) {
	c := newTestCollector("high")
	obj1 := vulnSummary("scan-1", statusCompleted, 1, 3, 0)
	c.reconcile(obj1, "vulnerability")

	// Next scan shows fewer findings — don't page on-call.
	obj2 := vulnSummary("scan-1", statusCompleted, 2, 1, 0)
	_, fire := c.reconcile(obj2, "vulnerability")
	if fire {
		t.Fatal("should not fire when finding count decreased (findings resolved)")
	}
}

func TestReconcile_EventFieldsPopulated(t *testing.T) {
	c := newTestCollector("high")
	obj := vulnSummary("kubescape-deployment-payments-api-app", statusCompleted, 1, 1, 2)
	ev, fire := c.reconcile(obj, "vulnerability")
	if !fire {
		t.Fatal("expected event")
	}
	if ev.Namespace != "production" {
		t.Errorf("want namespace production, got %q", ev.Namespace)
	}
	if ev.Workload != "Deployment/payments-api" {
		t.Errorf("want workload Deployment/payments-api, got %q", ev.Workload)
	}
	if ev.Kind != "vulnerability" {
		t.Errorf("want kind vulnerability, got %q", ev.Kind)
	}
	if ev.Title == "" {
		t.Error("title must not be empty")
	}
}
