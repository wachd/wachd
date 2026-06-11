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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"time"

	"github.com/wachd/wachd/internal/agent"
)

// Forwarder posts Events to the central Wachd webhook endpoint.
// It uses the existing /api/v1/webhook/{teamID}/{secret} path so no new
// server-side API is required.
type Forwarder struct {
	endpoint string // e.g. https://wachd.company.internal
	teamID   string
	secret   string
	cluster  string // optional — injected into every outgoing event's labels
	client   *http.Client
}

func newForwarder(endpoint, teamID, secret, cluster string) *Forwarder {
	return &Forwarder{
		endpoint: endpoint,
		teamID:   teamID,
		secret:   secret,
		cluster:  cluster,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

type webhookPayload struct {
	Title    string            `json:"title"`
	Message  string            `json:"message,omitempty"`
	Severity string            `json:"severity"`
	Source   string            `json:"source"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// send posts a single Event to central Wachd. Returns an error on non-2xx or network failure.
func (f *Forwarder) send(ctx context.Context, ev agent.Event) error {
	labels := ev.Labels
	if f.cluster != "" {
		labels = make(map[string]string, len(ev.Labels)+1)
		maps.Copy(labels, ev.Labels)
		labels["cluster"] = f.cluster
	}
	p := webhookPayload{
		Title:    ev.Title,
		Message:  ev.Details,
		Severity: ev.Severity,
		Source:   ev.Source,
		Labels:   labels,
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/webhook/%s/%s", f.endpoint, f.teamID, f.secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, f.endpoint)
	}
	return nil
}

// Run drains ch and forwards each Event to central Wachd.
// Send errors are logged but never crash the agent — fail open.
func (f *Forwarder) Run(ctx context.Context, ch <-chan agent.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := f.send(ctx, ev); err != nil {
				log.Printf("warn: forward %q: %v", ev.Title, err)
			} else {
				log.Printf("→ forwarded [%s/%s] %s", ev.Source, ev.Severity, ev.Title)
			}
		}
	}
}
