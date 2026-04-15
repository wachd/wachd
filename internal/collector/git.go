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
	SHA       string    `json:"sha"`
	Message   string    `json:"message"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
	Files     []string  `json:"files"`
}

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
	// Parse owner and repo from "owner/repo" format
	owner, repoName, err := parseRepo(repo)
	if err != nil {
		return nil, err
	}

	// Fetch commits
	opts := &github.CommitsListOptions{
		SHA:   branch,
		Since: since,
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

		// Get files changed in this commit (requires additional API call)
		// Only fetch if we need detailed file info
		if c.Files != nil {
			files := make([]string, 0, len(c.Files))
			for _, f := range c.Files {
				files = append(files, f.GetFilename())
			}
			commit.Files = files
		}

		result = append(result, commit)
	}

	return result, nil
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
