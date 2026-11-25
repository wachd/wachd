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
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
)

// GitCollector fetches git commits from GitHub
type GitCollector struct {
	client *github.Client
	token  string
}

// Commit represents a git commit
type Commit struct {
	SHA       string       `json:"sha"`
	Message   string       `json:"message"`
	Author    string       `json:"author"`
	Timestamp time.Time    `json:"timestamp"`
	URL       string       `json:"url"`
	Files     []string     `json:"files"`
	Changes   []FileChange `json:"changes,omitempty"`
	// Relation says how the commit relates to the incident: RelationWithinWindow
	// or RelationPriorToIncident. Empty for commits fetched without an incident.
	Relation string `json:"relation,omitempty"`
}

const (
	// RelationWithinWindow marks a commit made inside the normal lookback window.
	RelationWithinWindow = "within_window"
	// RelationPriorToIncident marks a fallback commit older than the window.
	RelationPriorToIncident = "prior_to_incident"
)

// fallbackCommitLimit bounds how many older commits the fallback returns.
const fallbackCommitLimit = 3

// FileChange contains bounded, sanitized patch evidence for a commit.
type FileChange struct {
	Filename string `json:"filename"`
	Patch    string `json:"patch,omitempty"`
}

var secretPatchRE = regexp.MustCompile(`(?i)(token|password|secret|api[_-]?key)\s*[:=]\s*[^\s]+`)

// NewGitCollector creates a new GitHub collector
func NewGitCollector(token string) *GitCollector {
	var client *github.Client
	if token != "" {
		client = github.NewClient(nil).WithAuthToken(token)
	} else {
		client = github.NewClient(nil)
	}

	return &GitCollector{
		client: client,
		token:  token,
	}
}

// FetchRecentCommits fetches recent commits from a repository
// repo format: "owner/repo", branch: "main", since: time to fetch from
func (g *GitCollector) FetchRecentCommits(ctx context.Context, repo, branch string, since time.Time, limit int) ([]Commit, error) {
	return g.listCommits(ctx, repo, branch, since, time.Time{}, limit)
}

// FetchCommitsForIncident returns commits made in [firedAt-window, firedAt].
// When that window is empty it falls back to the newest commits made before
// firedAt and no older than maxAge, so a configuration change deployed hours
// or days earlier is still available as evidence. Commits made after firedAt
// are never returned. Results are newest first.
func (g *GitCollector) FetchCommitsForIncident(ctx context.Context, repo, branch string, firedAt time.Time, window, maxAge time.Duration, limit int) ([]Commit, error) {
	commits, err := g.listCommits(ctx, repo, branch, firedAt.Add(-window), firedAt, limit)
	if err != nil {
		return nil, err
	}
	if len(commits) > 0 {
		for i := range commits {
			commits[i].Relation = RelationWithinWindow
		}
		return commits, nil
	}

	if maxAge <= window {
		return commits, nil
	}
	fallbackLimit := fallbackCommitLimit
	if limit < fallbackLimit {
		fallbackLimit = limit
	}
	commits, err = g.listCommits(ctx, repo, branch, firedAt.Add(-maxAge), firedAt, fallbackLimit)
	if err != nil {
		return nil, err
	}
	for i := range commits {
		commits[i].Relation = RelationPriorToIncident
	}
	return commits, nil
}

func (g *GitCollector) listCommits(ctx context.Context, repo, branch string, since, until time.Time, limit int) ([]Commit, error) {
	// Parse owner and repo from "owner/repo" format
	owner, repoName, err := parseRepo(repo)
	if err != nil {
		return nil, err
	}

	// Fetch commits
	opts := &github.CommitsListOptions{
		SHA:   branch,
		Since: since,
		Until: until,
		ListOptions: github.ListOptions{
			PerPage: limit,
		},
	}

	commits, _, err := g.client.Repositories.ListCommits(ctx, owner, repoName, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch commits: %w", err)
	}

	// Convert to our Commit type
	result := make([]Commit, 0, len(commits))
	for _, c := range commits {
		if c.Commit == nil {
			continue
		}

		commit := Commit{
			SHA:     c.GetSHA(),
			Message: c.GetCommit().GetMessage(),
			URL:     c.GetHTMLURL(),
		}

		if c.Commit.Author != nil {
			commit.Author = c.Commit.Author.GetName()
			commit.Timestamp = c.Commit.Author.GetDate().Time
		}

		// ListCommits does not reliably include files; fetch bounded commit details.
		detail, _, detailErr := g.client.Repositories.GetCommit(ctx, owner, repoName, commit.SHA, nil)
		if detailErr == nil && detail != nil {
			const maxFiles, maxPatch = 20, 12000
			for i, f := range detail.Files {
				if i >= maxFiles || f == nil {
					break
				}
				name := f.GetFilename()
				commit.Files = append(commit.Files, name)
				patch := sanitizePatch(f.GetPatch())
				if len(patch) > maxPatch {
					patch = patch[:maxPatch]
				}
				commit.Changes = append(commit.Changes, FileChange{Filename: name, Patch: patch})
			}
		}

		result = append(result, commit)
	}

	return result, nil
}

func sanitizePatch(patch string) string {
	patch = secretPatchRE.ReplaceAllString(patch, "$1=[REDACTED]")
	if strings.TrimSpace(patch) == "" {
		return ""
	}
	return patch
}

// parseRepo parses "owner/repo" format into owner and repo
func parseRepo(repo string) (string, string, error) {
	var owner, name string
	for i, ch := range repo {
		if ch == '/' {
			owner = repo[:i]
			name = repo[i+1:]
			break
		}
	}

	if owner == "" || name == "" {
		return "", "", fmt.Errorf("invalid repo format, expected 'owner/repo', got '%s'", repo)
	}

	return owner, name, nil
}
