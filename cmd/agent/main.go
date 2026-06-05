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

// wachd-agent runs in a customer cluster and forwards security findings to
// the central Wachd instance for RCA, on-call routing, and escalation.
//
// Required environment variables:
//
//	WACHD_ENDPOINT       — base URL of central Wachd, e.g. https://wachd.company.internal
//	WACHD_TEAM_ID        — team ID to route findings to
//	WACHD_WEBHOOK_SECRET — webhook secret for the team
//
// Optional:
//
//	KUBESCAPE_ENABLED    — set to "false" to disable the Kubescape collector (default: enabled)
//	KUBESCAPE_NAMESPACE  — namespace where Kubescape is installed (default: "kubescape")
//	KUBESCAPE_MIN_SEVERITY — minimum severity to fire on: "high" or "critical" (default: "high")
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wachd/wachd/cmd/agent/collectors/kubescape"
	"github.com/wachd/wachd/internal/agent"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	endpoint := mustEnv("WACHD_ENDPOINT")
	teamID := mustEnv("WACHD_TEAM_ID")
	secret := mustEnv("WACHD_WEBHOOK_SECRET")

	fwd := newForwarder(endpoint, teamID, secret)

	// merged receives events from all active collectors.
	merged := make(chan agent.Event, 64)

	if os.Getenv("KUBESCAPE_ENABLED") != "false" {
		ks, err := kubescape.New()
		if err != nil {
			log.Fatalf("kubescape collector: %v", err)
		}
		ch, err := ks.Start(ctx)
		if err != nil {
			log.Fatalf("kubescape collector start: %v", err)
		}
		go fanIn(ctx, ch, merged)
		log.Printf("kubescape collector started (namespace: %s)", os.Getenv("KUBESCAPE_NAMESPACE"))
	}

	log.Printf("wachd-agent started — endpoint: %s, team: %s", endpoint, teamID)
	fwd.Run(ctx, merged)
	log.Printf("wachd-agent stopped")
}

// fanIn drains src and writes to dst until ctx is cancelled or src is closed.
func fanIn(ctx context.Context, src <-chan agent.Event, dst chan<- agent.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-src:
			if !ok {
				return
			}
			select {
			case dst <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return v
}
