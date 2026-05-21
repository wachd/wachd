// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestScoreNodeSimilarityUsesIssue36Signals(t *testing.T) {
	source := testIncidentNode(t, "Checkout 500 errors after deploy", map[string]any{
		"service":        "checkout-api",
		"root_cause":     "database connection pool exhausted",
		"log_patterns":   []string{"connection refused from postgres", "timeout acquiring database connection"},
		"metric_anomaly": "5xx rate increased above threshold",
		"commit":         "abc123 changed checkout connection pooling",
	})

	similar := testIncidentNode(t, "Checkout API database connection failures", map[string]any{
		"service":      "checkout-api",
		"root_cause":   "postgres connection pool exhausted after deploy",
		"log_patterns": []string{"database connection refused", "timeout waiting for postgres connection"},
		"resolution":   "increased postgres max connections and rolled back pooling change",
	})

	unrelated := testIncidentNode(t, "Email delivery delayed", map[string]any{
		"service":      "email-worker",
		"root_cause":   "third-party SMTP provider throttling",
		"log_patterns": []string{"smtp 421 rate limited"},
		"resolution":   "queued retries and changed provider limit",
	})

	similarScore := scoreNodeSimilarity(source, similar)
	unrelatedScore := scoreNodeSimilarity(source, unrelated)

	if similarScore.Score <= unrelatedScore.Score {
		t.Fatalf("expected similar incident score %.3f to be higher than unrelated score %.3f", similarScore.Score, unrelatedScore.Score)
	}

	if similarScore.Score < 0.55 {
		t.Fatalf("expected strong similarity score, got %.3f (%s)", similarScore.Score, similarScore.Reason)
	}

	if !strings.Contains(similarScore.Reason, "same affected service") {
		t.Fatalf("expected affected service in reason, got %q", similarScore.Reason)
	}

	if !strings.Contains(similarScore.Reason, "similar log pattern") {
		t.Fatalf("expected log pattern in reason, got %q", similarScore.Reason)
	}
}

func TestScoreNodeSimilarityFallsBackToIncidentTitle(t *testing.T) {
	source := testIncidentNode(t, "High CPU usage on web server", nil)
	similar := testIncidentNode(t, "CPU usage high on web-server", nil)
	unrelated := testIncidentNode(t, "Database connection refused", nil)

	similarScore := scoreNodeSimilarity(source, similar)
	unrelatedScore := scoreNodeSimilarity(source, unrelated)

	if similarScore.Score <= unrelatedScore.Score {
		t.Fatalf("expected title match %.3f to beat unrelated %.3f", similarScore.Score, unrelatedScore.Score)
	}

	if similarScore.Score == 0 {
		t.Fatal("expected non-zero title similarity score")
	}
}

func TestScoreNodeSimilarityDoesNotLetServiceAloneDominate(t *testing.T) {
	source := testIncidentNode(t, "Checkout 500 errors", map[string]any{
		"service":      "checkout-api",
		"root_cause":   "database connection pool exhausted",
		"log_patterns": []string{"postgres connection refused"},
	})

	weakMatch := testIncidentNode(t, "Checkout cache warmup slow", map[string]any{
		"service":      "checkout-api",
		"root_cause":   "large cache refill after deploy",
		"log_patterns": []string{"redis cache miss"},
	})

	strongMatch := testIncidentNode(t, "Checkout database connection failures", map[string]any{
		"service":      "checkout-api",
		"root_cause":   "postgres connection pool exhausted",
		"log_patterns": []string{"database connection refused"},
	})

	weakScore := scoreNodeSimilarity(source, weakMatch)
	strongScore := scoreNodeSimilarity(source, strongMatch)

	if weakScore.Score >= strongScore.Score {
		t.Fatalf("expected strong match %.3f to beat service-only weak match %.3f", strongScore.Score, weakScore.Score)
	}
}

func testIncidentNode(t *testing.T, label string, props map[string]any) *Node {
	t.Helper()

	var raw json.RawMessage
	if props != nil {
		encoded, err := json.Marshal(props)
		if err != nil {
			t.Fatalf("marshal properties: %v", err)
		}
		raw = encoded
	}

	return &Node{
		ID:         uuid.New(),
		TeamID:     uuid.New(),
		Type:       NodeTypeIncident,
		Status:     NodeStatusPermanent,
		Label:      label,
		Properties: raw,
	}
}
