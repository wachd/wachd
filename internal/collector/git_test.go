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

package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
)

func TestSanitizePatch_RedactsSecretsAndPreservesConfigDiff(t *testing.T) {
	patch := "-DB_POOL_MAX=50\n+DB_POOL_MAX=5\n+API_TOKEN=ghp_secretvalue"
	got := sanitizePatch(patch)
	if !strings.Contains(got, "DB_POOL_MAX=50") || !strings.Contains(got, "DB_POOL_MAX=5") {
		t.Fatalf("configuration change was removed: %q", got)
	}
	if strings.Contains(got, "ghp_secretvalue") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("secret was not redacted: %q", got)
	}
}

func TestParseRepo_Valid(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
	}{
		{"owner/repo", "owner", "repo"},
		{"my-org/my-service", "my-org", "my-service"},
		{"acme/checkout-api", "acme", "checkout-api"},
	}

	for _, tt := range tests {
		owner, repo, err := parseRepo(tt.input)
		if err != nil {
			t.Errorf("parseRepo(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if owner != tt.wantOwner {
			t.Errorf("parseRepo(%q) owner = %q, want %q", tt.input, owner, tt.wantOwner)
		}
		if repo != tt.wantRepo {
			t.Errorf("parseRepo(%q) repo = %q, want %q", tt.input, repo, tt.wantRepo)
		}
	}
}

func TestParseRepo_Invalid(t *testing.T) {
	cases := []string{
		"",
		"nodash",
		"/noowner",
		"noname/",
	}

	for _, input := range cases {
		_, _, err := parseRepo(input)
		if err == nil {
			t.Errorf("parseRepo(%q) expected error, got nil", input)
		}
	}
}

func TestNewGitCollector_WithToken(t *testing.T) {
	c := NewGitCollector("ghp_testtoken")
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
}

func TestNewGitCollector_NoToken(t *testing.T) {
	c := NewGitCollector("")
	if c == nil {
		t.Fatal("expected non-nil collector with empty token")
	}
}

// fakeGitHub serves a fixed commit history, honouring since/until and per_page.
func fakeGitHub(t *testing.T, history []map[string]any) *GitCollector {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/acme/checkout/commits", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		since, _ := time.Parse(time.RFC3339, q.Get("since"))
		until, _ := time.Parse(time.RFC3339, q.Get("until"))
		var out []map[string]any
		for _, c := range history {
			ts := c["_ts"].(time.Time)
			if !since.IsZero() && ts.Before(since) {
				continue
			}
			if !until.IsZero() && ts.After(until) {
				continue
			}
			out = append(out, map[string]any{
				"sha":      c["sha"],
				"html_url": "https://example.test/" + c["sha"].(string),
				"commit": map[string]any{
					"message": c["message"],
					"author":  map[string]any{"name": "dev", "date": ts.Format(time.RFC3339)},
				},
			})
		}
		if n := q.Get("per_page"); n == "1" && len(out) > 1 {
			out = out[:1]
		}
		if n := q.Get("per_page"); n == "3" && len(out) > 3 {
			out = out[:3]
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/api/v3/repos/acme/checkout/commits/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha": "x",
			"files": []map[string]any{{
				"filename": "deploy/checkout-api.env",
				"patch":    "@@ -1 +1 @@\n-DB_POOL_MAX=50\n+DB_POOL_MAX=5",
			}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL + "/api/v3/")
	client := github.NewClient(nil)
	client.BaseURL = base
	return &GitCollector{client: client}
}

func commitAt(sha string, ts time.Time) map[string]any {
	return map[string]any{"sha": sha, "message": "config: " + sha, "_ts": ts}
}

func TestFetchCommitsForIncident_FallsBackToOlderCommitWithPatch(t *testing.T) {
	fired := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	g := fakeGitHub(t, []map[string]any{
		commitAt("newest-after-incident", fired.Add(time.Hour)),
		commitAt("d2fd87ca", fired.Add(-21*time.Hour)),
		commitAt("cb2b699e", fired.Add(-22*time.Hour)),
		commitAt("ancient", fired.Add(-30*24*time.Hour)),
	})

	got, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", fired, 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SHA != "d2fd87ca" {
		t.Fatalf("expected newest pre-incident commit first, got %+v", got)
	}
	for _, c := range got {
		if c.Relation != RelationPriorToIncident {
			t.Errorf("%s relation = %q, want %q", c.SHA, c.Relation, RelationPriorToIncident)
		}
		if c.Timestamp.After(fired) {
			t.Errorf("%s was committed after the incident", c.SHA)
		}
	}
	if len(got[0].Changes) != 1 || !strings.Contains(got[0].Changes[0].Patch, "-DB_POOL_MAX=50") || !strings.Contains(got[0].Changes[0].Patch, "+DB_POOL_MAX=5") {
		t.Fatalf("patch evidence missing: %+v", got[0].Changes)
	}
}

func TestFetchCommitsForIncident_PrefersWindowCommits(t *testing.T) {
	fired := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	g := fakeGitHub(t, []map[string]any{
		commitAt("recent", fired.Add(-10*time.Minute)),
		commitAt("older", fired.Add(-5*time.Hour)),
	})
	got, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", fired, 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SHA != "recent" || got[0].Relation != RelationWithinWindow {
		t.Fatalf("expected only the in-window commit, got %+v", got)
	}
}

func TestFetchCommitsForIncident_FallbackIsBounded(t *testing.T) {
	fired := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	var history []map[string]any
	for i := 1; i <= 8; i++ {
		history = append(history, commitAt("c"+string(rune('0'+i)), fired.Add(-time.Duration(i)*time.Hour)))
	}
	history = append(history, commitAt("too-old", fired.Add(-8*24*time.Hour)))
	g := fakeGitHub(t, history)

	got, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", fired, 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("fallback should return at most 3 commits, got %d", len(got))
	}
	none, err := g.FetchCommitsForIncident(context.Background(), "acme/checkout", "main", fired.Add(30*24*time.Hour), 30*time.Minute, 7*24*time.Hour, 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("commits older than maxAge must not be returned: %v %+v", err, none)
	}
}
