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
	"strings"
	"testing"
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
