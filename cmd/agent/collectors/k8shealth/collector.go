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

// Package k8shealth implements a wachd-agent collector that watches Kubernetes
// pod and node health and emits Events for actionable failure conditions —
// without requiring Grafana, Prometheus, or any external monitoring tool.
//
// Pod conditions detected:
//   - CrashLoopBackOff — container repeatedly crashing
//   - OOMKilled        — container killed by the OOM killer
//   - ImagePullBackOff / ErrImagePull — image cannot be pulled
//   - Pending > threshold — pod stuck in Pending (default 15 min)
//
// Node conditions detected:
//   - Ready=False / Unknown — node not reachable (critical)
//   - MemoryPressure=True   — node low on memory (high)
//   - DiskPressure=True     — node low on disk (high)
//   - PIDPressure=True      — node low on PIDs (high)
//
// State is tracked in memory to avoid re-firing for the same active condition.
// When a pod recovers (all containers ready) its state entries are cleared so
// a future crash re-fires correctly.
package k8shealth

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/wachd/wachd/internal/agent"
)

// Collector watches pod and node health across all namespaces.
type Collector struct {
	client         kubernetes.Interface
	pendingMinutes int
	mu             sync.Mutex
	seen           map[string]struct{}
}

// New creates a Collector using in-cluster config, falling back to KUBECONFIG.
//
// Env vars:
//
//	K8SHEALTH_PENDING_MINUTES — minutes before a Pending pod fires (default: 15)
func New() (*Collector, error) {
	cfg, err := loadKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}
	return newWithClient(client), nil
}

// newWithClient creates a Collector with an injected client — used in tests.
func newWithClient(client kubernetes.Interface) *Collector {
	pendingMin := 15
	if s := os.Getenv("K8SHEALTH_PENDING_MINUTES"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			pendingMin = v
		}
	}
	return &Collector{
		client:         client,
		pendingMinutes: pendingMin,
		seen:           make(map[string]struct{}),
	}
}

func (c *Collector) Name() string { return "k8shealth" }

// PendingMinutes returns the configured threshold for stuck-pending pod alerts.
func (c *Collector) PendingMinutes() int { return c.pendingMinutes }

// Start launches watch goroutines for pods and nodes, and a pending-pod poller.
// Returns a merged Event channel closed when ctx is cancelled.
func (c *Collector) Start(ctx context.Context) (<-chan agent.Event, error) {
	ch := make(chan agent.Event, 64)
	go c.watchPods(ctx, ch)
	go c.watchNodes(ctx, ch)
	go c.pollPending(ctx, ch)
	return ch, nil
}

// --- pod watcher ----------------------------------------------------------

func (c *Collector) watchPods(ctx context.Context, out chan<- agent.Event) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runPodWatch(ctx, out); err != nil {
			log.Printf("k8shealth: pod watch error: %v — retrying in 15s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}
}

func (c *Collector) runPodWatch(ctx context.Context, out chan<- agent.Event) error {
	watcher, err := c.client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("watch pods: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case we, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("pod watch channel closed")
			}
			if we.Type != watch.Added && we.Type != watch.Modified {
				continue
			}
			pod, ok := we.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if ev, fire := c.reconcilePod(pod); fire {
				select {
				case out <- ev:
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// reconcilePod inspects container states and returns an Event for the first
// actionable failure found. Returns false when nothing new needs firing.
// On full recovery (all containers ready) it clears state so re-crashes re-fire.
func (c *Collector) reconcilePod(pod *corev1.Pod) (agent.Event, bool) {
	if pod.Status.Phase == corev1.PodRunning && allContainersReady(pod) {
		c.clearPod(pod.Namespace, pod.Name)
		return agent.Event{}, false
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			switch reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
				key := fmt.Sprintf("pod/%s/%s/%s/%s", pod.Namespace, pod.Name, cs.Name, reason)
				if c.markSeen(key) {
					return agent.Event{
						Source:    "k8shealth",
						Kind:      podReasonKind(reason),
						Severity:  "high",
						Title:     fmt.Sprintf("%s: %s in %s/%s (%s)", reason, cs.Name, pod.Namespace, pod.Name, workloadRef(pod)),
						Namespace: pod.Namespace,
						Workload:  workloadRef(pod),
						Container: cs.Name,
						Labels:    pod.Labels,
					}, true
				}
			}
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			// Key includes start time so each distinct OOM event fires once.
			key := fmt.Sprintf("pod/%s/%s/%s/OOMKilled/%d", pod.Namespace, pod.Name, cs.Name, cs.State.Terminated.StartedAt.UnixNano())
			if c.markSeen(key) {
				return agent.Event{
					Source:    "k8shealth",
					Kind:      "pod-oom",
					Severity:  "high",
					Title:     fmt.Sprintf("OOMKilled: %s in %s/%s (%s)", cs.Name, pod.Namespace, pod.Name, workloadRef(pod)),
					Namespace: pod.Namespace,
					Workload:  workloadRef(pod),
					Container: cs.Name,
					Labels:    pod.Labels,
				}, true
			}
		}
	}
	return agent.Event{}, false
}

// --- node watcher ---------------------------------------------------------

func (c *Collector) watchNodes(ctx context.Context, out chan<- agent.Event) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runNodeWatch(ctx, out); err != nil {
			log.Printf("k8shealth: node watch error: %v — retrying in 15s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}
}

func (c *Collector) runNodeWatch(ctx context.Context, out chan<- agent.Event) error {
	watcher, err := c.client.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("watch nodes: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case we, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("node watch channel closed")
			}
			if we.Type != watch.Added && we.Type != watch.Modified {
				continue
			}
			node, ok := we.Object.(*corev1.Node)
			if !ok {
				continue
			}
			for _, ev := range c.reconcileNode(node) {
				select {
				case out <- ev:
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// reconcileNode returns Events for any newly-active bad conditions on a node.
// When a condition clears it removes the key from seen so re-occurrence re-fires.
func (c *Collector) reconcileNode(node *corev1.Node) []agent.Event {
	var evs []agent.Event
	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			key := fmt.Sprintf("node/%s/NotReady", node.Name)
			if cond.Status == corev1.ConditionFalse || cond.Status == corev1.ConditionUnknown {
				if c.markSeen(key) {
					evs = append(evs, agent.Event{
						Source:   "k8shealth",
						Kind:     "node-notready",
						Severity: "critical",
						Title:    fmt.Sprintf("Node NotReady: %s (%s)", node.Name, cond.Reason),
						Workload: "Node/" + node.Name,
						Labels:   node.Labels,
					})
				}
			} else {
				c.clearKey(key)
			}
		case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
			key := fmt.Sprintf("node/%s/%s", node.Name, cond.Type)
			if cond.Status == corev1.ConditionTrue {
				if c.markSeen(key) {
					evs = append(evs, agent.Event{
						Source:   "k8shealth",
						Kind:     "node-pressure",
						Severity: "high",
						Title:    fmt.Sprintf("Node %s: %s (%s)", cond.Type, node.Name, cond.Reason),
						Workload: "Node/" + node.Name,
						Labels:   node.Labels,
					})
				}
			} else {
				c.clearKey(key)
			}
		}
	}
	return evs
}

// --- pending pod poller ---------------------------------------------------

func (c *Collector) pollPending(ctx context.Context, out chan<- agent.Event) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkPending(ctx, out)
		}
	}
}

func (c *Collector) checkPending(ctx context.Context, out chan<- agent.Event) {
	list, err := c.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		log.Printf("k8shealth: list pending pods: %v", err)
		return
	}
	threshold := time.Duration(c.pendingMinutes) * time.Minute
	for i := range list.Items {
		pod := &list.Items[i]
		age := time.Since(pod.CreationTimestamp.Time)
		if age < threshold {
			continue
		}
		key := fmt.Sprintf("pod/%s/%s/Pending", pod.Namespace, pod.Name)
		if c.markSeen(key) {
			ev := agent.Event{
				Source:    "k8shealth",
				Kind:      "pod-pending",
				Severity:  "high",
				Title:     fmt.Sprintf("Pod stuck Pending %dm: %s/%s (%s)", int(age.Minutes()), pod.Namespace, pod.Name, workloadRef(pod)),
				Namespace: pod.Namespace,
				Workload:  workloadRef(pod),
				Labels:    pod.Labels,
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

// --- state helpers --------------------------------------------------------

// markSeen records key and returns true only the first time. Thread-safe.
func (c *Collector) markSeen(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[key]; ok {
		return false
	}
	c.seen[key] = struct{}{}
	return true
}

func (c *Collector) clearKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.seen, key)
}

// clearPod removes all seen entries for a given pod (called on recovery).
func (c *Collector) clearPod(ns, name string) {
	prefix := fmt.Sprintf("pod/%s/%s/", ns, name)
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.seen {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.seen, k)
		}
	}
}

// --- k8s helpers ----------------------------------------------------------

// workloadRef returns the controlling owner reference as "Kind/Name" for a pod,
// falling back to "Pod/<name>" when there is no owner.
func workloadRef(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return fmt.Sprintf("%s/%s", ref.Kind, ref.Name)
		}
	}
	return fmt.Sprintf("Pod/%s", pod.Name)
}

func allContainersReady(pod *corev1.Pod) bool {
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

func podReasonKind(reason string) string {
	switch reason {
	case "CrashLoopBackOff":
		return "pod-crash"
	case "ImagePullBackOff", "ErrImagePull":
		return "pod-imagepull"
	default:
		return "pod-unknown"
	}
}

func loadKubeConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.ExpandEnv("$HOME/.kube/config")
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
