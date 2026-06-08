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

package agent

import "context"

// Event is a normalised security finding forwarded to central Wachd.
// Each collector maps its source-specific data into this struct before sending.
type Event struct {
	// Source identifies the collector that produced this event, e.g. "kubescape".
	Source string

	// Kind is the finding category, e.g. "vulnerability" or "misconfiguration".
	Kind string

	// Severity is the highest severity in this batch: "critical" or "high".
	Severity string

	// Title is a human-readable one-line summary of the finding.
	Title string

	// Namespace is the Kubernetes namespace of the affected workload.
	Namespace string

	// Workload is "<Kind>/<Name>" of the affected workload, e.g. "Deployment/payments-api".
	Workload string

	// Container is the container name within the workload, if applicable.
	Container string

	// Details is the alert body — fetched from the raw CRD and trimmed to avoid noise.
	Details string

	// Labels carries source-specific routing dimensions, e.g. kubescape.io/* labels.
	Labels map[string]string
}

// Collector watches a security tool and streams Events until ctx is cancelled.
// Implementations must fail open: watch/parse errors are logged, not returned,
// and the channel is never closed on transient errors.
type Collector interface {
	// Name returns the collector identifier, e.g. "kubescape".
	Name() string

	// Start begins watching and returns a channel of Events.
	// The channel is closed when ctx is cancelled.
	Start(ctx context.Context) (<-chan Event, error)
}
