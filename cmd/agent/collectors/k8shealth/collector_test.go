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

package k8shealth

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCollector() *Collector {
	return &Collector{
		pendingMinutes: 15,
		seen:           make(map[string]struct{}),
	}
}

func pod(ns, name string, cs []corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PodStatus{ContainerStatuses: cs},
	}
}

func waitingCS(name, reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}},
	}
}

func terminatedCS(name, reason string, t time.Time) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				Reason:    reason,
				StartedAt: metav1.NewTime(t),
			},
		},
	}
}

func readyCS(name string) corev1.ContainerStatus {
	return corev1.ContainerStatus{Name: name, Ready: true}
}

// --- reconcilePod tests ---

func TestReconcilePod_CrashLoopBackOff(t *testing.T) {
	c := newCollector()
	p := pod("default", "api", []corev1.ContainerStatus{waitingCS("app", "CrashLoopBackOff")})

	ev, fire := c.reconcilePod(p)
	if !fire {
		t.Fatal("expected event to fire")
	}
	if ev.Kind != "pod-crash" {
		t.Errorf("kind = %q, want pod-crash", ev.Kind)
	}
	if ev.Severity != "high" {
		t.Errorf("severity = %q, want high", ev.Severity)
	}
	if ev.Source != "k8shealth" {
		t.Errorf("source = %q, want k8shealth", ev.Source)
	}

	// Second call must not re-fire.
	_, fire2 := c.reconcilePod(p)
	if fire2 {
		t.Fatal("expected no re-fire for same active condition")
	}
}

func TestReconcilePod_ImagePullBackOff(t *testing.T) {
	c := newCollector()
	p := pod("kube-system", "worker", []corev1.ContainerStatus{waitingCS("w", "ImagePullBackOff")})

	ev, fire := c.reconcilePod(p)
	if !fire {
		t.Fatal("expected event to fire")
	}
	if ev.Kind != "pod-imagepull" {
		t.Errorf("kind = %q, want pod-imagepull", ev.Kind)
	}
}

func TestReconcilePod_OOMKilled(t *testing.T) {
	c := newCollector()
	startTime := time.Now()
	p := pod("prod", "payment", []corev1.ContainerStatus{terminatedCS("app", "OOMKilled", startTime)})

	ev, fire := c.reconcilePod(p)
	if !fire {
		t.Fatal("expected event to fire")
	}
	if ev.Kind != "pod-oom" {
		t.Errorf("kind = %q, want pod-oom", ev.Kind)
	}

	// Same start time — must not re-fire.
	_, fire2 := c.reconcilePod(p)
	if fire2 {
		t.Fatal("expected no re-fire for same OOM event")
	}

	// New OOM with different start time must re-fire.
	p2 := pod("prod", "payment", []corev1.ContainerStatus{terminatedCS("app", "OOMKilled", startTime.Add(time.Second))})
	_, fire3 := c.reconcilePod(p2)
	if !fire3 {
		t.Fatal("expected re-fire for new OOM event")
	}
}

func TestReconcilePod_RecoveryClears_ThenReCrashFires(t *testing.T) {
	c := newCollector()
	p := pod("default", "api", []corev1.ContainerStatus{waitingCS("app", "CrashLoopBackOff")})

	_, fire := c.reconcilePod(p)
	if !fire {
		t.Fatal("expected initial fire")
	}

	// Pod recovers.
	recovered := pod("default", "api", []corev1.ContainerStatus{readyCS("app")})
	recovered.Status.Phase = corev1.PodRunning
	_, fire2 := c.reconcilePod(recovered)
	if fire2 {
		t.Fatal("recovery must not fire")
	}

	// Pod crashes again — must re-fire.
	_, fire3 := c.reconcilePod(p)
	if !fire3 {
		t.Fatal("expected re-fire after recovery")
	}
}

func TestReconcilePod_NoContainerStatuses_NoFire(t *testing.T) {
	c := newCollector()
	p := pod("default", "init-pod", nil)
	_, fire := c.reconcilePod(p)
	if fire {
		t.Fatal("expected no fire for pod with no container statuses")
	}
}

// --- reconcileNode tests ---

func node(name string, conditions []corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: conditions},
	}
}

func nodeCondition(ct corev1.NodeConditionType, status corev1.ConditionStatus, reason string) corev1.NodeCondition {
	return corev1.NodeCondition{Type: ct, Status: status, Reason: reason}
}

func TestReconcileNode_NotReady(t *testing.T) {
	c := newCollector()
	n := node("worker-1", []corev1.NodeCondition{
		nodeCondition(corev1.NodeReady, corev1.ConditionFalse, "KubeletNotReady"),
	})

	evs := c.reconcileNode(n)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Kind != "node-notready" {
		t.Errorf("kind = %q, want node-notready", evs[0].Kind)
	}
	if evs[0].Severity != "critical" {
		t.Errorf("severity = %q, want critical", evs[0].Severity)
	}

	// Must not re-fire.
	evs2 := c.reconcileNode(n)
	if len(evs2) != 0 {
		t.Fatalf("expected no re-fire, got %d events", len(evs2))
	}
}

func TestReconcileNode_DiskPressure(t *testing.T) {
	c := newCollector()
	n := node("worker-2", []corev1.NodeCondition{
		nodeCondition(corev1.NodeDiskPressure, corev1.ConditionTrue, "KubeletHasDiskPressure"),
	})

	evs := c.reconcileNode(n)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Kind != "node-pressure" {
		t.Errorf("kind = %q, want node-pressure", evs[0].Kind)
	}
	if evs[0].Severity != "high" {
		t.Errorf("severity = %q, want high", evs[0].Severity)
	}
}

func TestReconcileNode_RecoveryClears_ThenReFires(t *testing.T) {
	c := newCollector()
	n := node("worker-3", []corev1.NodeCondition{
		nodeCondition(corev1.NodeMemoryPressure, corev1.ConditionTrue, "KubeletHasMemoryPressure"),
	})

	evs := c.reconcileNode(n)
	if len(evs) != 1 {
		t.Fatal("expected initial fire")
	}

	// Condition clears.
	recovered := node("worker-3", []corev1.NodeCondition{
		nodeCondition(corev1.NodeMemoryPressure, corev1.ConditionFalse, ""),
	})
	evs2 := c.reconcileNode(recovered)
	if len(evs2) != 0 {
		t.Fatal("recovery must not fire")
	}

	// Condition returns — must re-fire.
	evs3 := c.reconcileNode(n)
	if len(evs3) != 1 {
		t.Fatal("expected re-fire after recovery")
	}
}

func TestReconcileNode_Unknown_NotReady(t *testing.T) {
	c := newCollector()
	n := node("worker-4", []corev1.NodeCondition{
		nodeCondition(corev1.NodeReady, corev1.ConditionUnknown, "NodeStatusUnknown"),
	})
	evs := c.reconcileNode(n)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event for Unknown ready condition, got %d", len(evs))
	}
	if evs[0].Severity != "critical" {
		t.Errorf("severity = %q, want critical", evs[0].Severity)
	}
}

// --- workloadRef tests ---

func TestWorkloadRef_WithOwner(t *testing.T) {
	ctrl := true
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-abc",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "api-xyz", Controller: &ctrl},
			},
		},
	}
	got := workloadRef(p)
	if got != "ReplicaSet/api-xyz" {
		t.Errorf("workloadRef = %q, want ReplicaSet/api-xyz", got)
	}
}

func TestWorkloadRef_NoOwner(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "standalone"}}
	got := workloadRef(p)
	if got != "Pod/standalone" {
		t.Errorf("workloadRef = %q, want Pod/standalone", got)
	}
}
