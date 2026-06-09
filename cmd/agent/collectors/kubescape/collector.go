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

// Package kubescape implements a wachd-agent collector that watches Kubescape
// CRDs and emits Events for net-new HIGH/CRITICAL security findings.
//
// It watches two summary CRD types in the spdx.softwarecomposition.kubescape.io
// API group:
//   - workloadconfigurationscansummaries — NSA/MITRE/CIS compliance findings
//   - vulnerabilitymanifestsummaries     — CVE findings, split into .all / .relevant
//
// Only .relevant severity counts are used for vulnerability findings — these are
// CVEs in packages actually loaded at runtime (via Kubescape's eBPF sensor), which
// cuts alert volume dramatically versus .all.
//
// Architecture reference: wachd/wachd discussions/63
package kubescape

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/wachd/wachd/internal/agent"
)

// Kubescape annotation and label keys — sourced from
// github.com/kubescape/storage tests/integration-test-suite/helpers.go
const (
	annotationStatus    = "kubescape.io/status"
	annotationWLID      = "kubescape.io/wlid"
	annotationImageID   = "kubescape.io/image-id"
	annotationContainer = "kubescape.io/workload-container-name"

	labelAPIGroup    = "kubescape.io/workload-api-group"
	labelAPIVersion  = "kubescape.io/workload-api-version"
	labelNamespace   = "kubescape.io/workload-namespace"
	labelKind        = "kubescape.io/workload-kind"
	labelName        = "kubescape.io/workload-name"
	labelContainer   = "kubescape.io/workload-container-name"
	labelContext     = "kubescape.io/context"

	statusCompleted = "ready"
)

var (
	configScanSummaryGVR = schema.GroupVersionResource{
		Group:    "spdx.softwarecomposition.kubescape.io",
		Version:  "v1beta1",
		Resource: "workloadconfigurationscansummaries",
	}
	vulnSummaryGVR = schema.GroupVersionResource{
		Group:    "spdx.softwarecomposition.kubescape.io",
		Version:  "v1beta1",
		Resource: "vulnerabilitymanifestsummaries",
	}
)

// State tracks last-seen finding counts per key to avoid re-firing on re-scans.
type State struct {
	mu      sync.Mutex
	entries map[string]int
}

func newState() *State {
	return &State{entries: make(map[string]int)}
}

// isNew reports whether value has increased for key. Updates stored value if so.
func (s *State) isNew(key string, value int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value > s.entries[key] {
		s.entries[key] = value
		return true
	}
	return false
}

// Collector watches Kubescape summary CRDs and emits Events for net-new findings.
type Collector struct {
	client      dynamic.Interface
	minSeverity string // threshold: "critical" or "high" (default: "high")
	state       *State
}

// New creates a Collector. It loads k8s config from in-cluster env if available,
// falling back to KUBECONFIG for local development.
func New() (*Collector, error) {
	cfg, err := loadKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}

	minSev := os.Getenv("KUBESCAPE_MIN_SEVERITY")
	if minSev != "critical" {
		minSev = "high"
	}

	return &Collector{
		client:      client,
		minSeverity: minSev,
		state:       newState(),
	}, nil
}

func (c *Collector) Name() string { return "kubescape" }

// Start launches watch goroutines for both summary CRD types and returns a merged Event channel.
// The channel is drained until ctx is cancelled.
func (c *Collector) Start(ctx context.Context) (<-chan agent.Event, error) {
	ch := make(chan agent.Event, 64)
	go c.watchSummaries(ctx, configScanSummaryGVR, "misconfiguration", ch)
	go c.watchSummaries(ctx, vulnSummaryGVR, "vulnerability", ch)
	return ch, nil
}

// watchSummaries runs the watch loop for a given GVR, reconnecting on errors.
func (c *Collector) watchSummaries(ctx context.Context, gvr schema.GroupVersionResource, kind string, out chan<- agent.Event) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.runWatch(ctx, gvr, kind, out); err != nil {
			log.Printf("kubescape: watch %s error: %v — retrying in 15s", gvr.Resource, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}
}

// runWatch lists existing summaries to prime dedup state, then opens a watch
// stream from the resulting ResourceVersion. On any error the caller retries.
func (c *Collector) runWatch(ctx context.Context, gvr schema.GroupVersionResource, kind string, out chan<- agent.Event) error {
	// List first — prime in-memory state so we don't re-fire on restart.
	list, err := c.client.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for i := range list.Items {
		c.reconcile(&list.Items[i], kind) // prime state only, discard event
	}
	rv := list.GetResourceVersion()

	watcher, err := c.client.Resource(gvr).Namespace("").Watch(ctx, metav1.ListOptions{
		ResourceVersion:     rv,
		AllowWatchBookmarks: true,
	})
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer watcher.Stop()

	log.Printf("kubescape: watching %s across all namespaces (rv=%s)", gvr.Resource, rv)

	for {
		select {
		case <-ctx.Done():
			return nil
		case we, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed")
			}
			if we.Type == watch.Bookmark {
				continue
			}
			if we.Type != watch.Added && we.Type != watch.Modified {
				continue
			}
			// Kubescape's custom storage backend sends watch events with
			// incomplete spec data — only metadata is reliable in the event.
			// Re-fetch the full object to get current severity counts.
			partial, ok := we.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			obj, err := c.client.Resource(gvr).Namespace(partial.GetNamespace()).Get(
				ctx, partial.GetName(), metav1.GetOptions{},
			)
			if err != nil {
				log.Printf("kubescape: get %s/%s: %v", partial.GetNamespace(), partial.GetName(), err)
				continue
			}
			ev, fire := c.reconcile(obj, kind)
			if !fire {
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// reconcile inspects a summary object and returns an Event if net-new HIGH/CRITICAL
// findings have appeared since the last reconcile for this workload.
func (c *Collector) reconcile(obj *unstructured.Unstructured, kind string) (agent.Event, bool) {
	annotations := obj.GetAnnotations()
	labels := obj.GetLabels()

	// Gate: only process completed scans.
	if annotations[annotationStatus] != statusCompleted {
		return agent.Event{}, false
	}

	// Extract routing dimensions from labels.
	ns := labels[labelNamespace]
	wlKind := labels[labelKind]
	wlName := labels[labelName]
	container := labels[labelContainer]

	// Count severity — use .relevant for vulns (eBPF runtime signal),
	// falling back to .all when node-agent is not running (field absent).
	// For config scans there is no all/relevant split.
	var critCount, highCount int
	if kind == "vulnerability" {
		critCount = relevantOrAll(obj, "critical")
		highCount = relevantOrAll(obj, "high")
	} else {
		critCount = nestedInt(obj, "spec", "severities", "critical")
		highCount = nestedInt(obj, "spec", "severities", "high")
	}

	// Apply minimum severity threshold.
	var severity string
	switch {
	case critCount > 0:
		severity = "critical"
	case highCount > 0 && c.minSeverity != "critical":
		severity = "high"
	default:
		return agent.Event{}, false
	}

	// Only fire if the total count has increased (not a re-scan of known findings).
	findingKey := fmt.Sprintf("%s/%s/%s/%s/%s", kind, ns, wlKind, wlName, container)
	total := critCount + highCount
	if !c.state.isNew(findingKey, total) {
		return agent.Event{}, false
	}

	title := fmt.Sprintf("Kubescape: %d %s finding(s) in %s/%s", total, kind, ns, wlName)
	if container != "" {
		title += fmt.Sprintf(" (%s)", container)
	}

	return agent.Event{
		Source:    "kubescape",
		Kind:      kind,
		Severity:  severity,
		Title:     title,
		Namespace: ns,
		Workload:  fmt.Sprintf("%s/%s", wlKind, wlName),
		Container: container,
		Labels:    labels,
	}, true
}

// relevantOrAll returns the .relevant severity count for vulnerability summaries.
// When node-agent (eBPF) is not running the .relevant field is absent entirely —
// in that case it falls back to .all so findings are not silently dropped.
// If .relevant exists and is 0 (eBPF confirmed no runtime exposure), 0 is returned.
func relevantOrAll(obj *unstructured.Unstructured, sev string) int {
	_, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "severities", sev, "relevant")
	if found {
		return nestedInt(obj, "spec", "severities", sev, "relevant")
	}
	return nestedInt(obj, "spec", "severities", sev, "all")
}

// nestedInt reads a numeric value from a nested path in an unstructured object.
// Handles both int64 (manually constructed objects, tests) and float64
// (JSON-decoded objects from the k8s watch stream). Returns 0 if not found.
func nestedInt(obj *unstructured.Unstructured, fields ...string) int {
	val, found, _ := unstructured.NestedFieldNoCopy(obj.Object, fields...)
	if !found {
		return 0
	}
	switch v := val.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// loadKubeConfig returns in-cluster config when running inside Kubernetes,
// falling back to KUBECONFIG / ~/.kube/config for local development.
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
