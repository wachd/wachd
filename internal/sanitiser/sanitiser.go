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

package sanitiser

import (
	"regexp"
)

// Sanitiser removes PII from text before AI processing
type Sanitiser struct {
	patterns []pattern
}

type pattern struct {
	regex       *regexp.Regexp
	replacement string
	description string
}

// NewSanitiser creates a new sanitiser with default patterns
func NewSanitiser() *Sanitiser {
	s := &Sanitiser{}
	s.loadDefaultPatterns()
	return s
}

// Sanitise removes all PII from the input text
func (s *Sanitiser) Sanitise(text string) string {
	result := text

	for _, p := range s.patterns {
		result = p.regex.ReplaceAllString(result, p.replacement)
	}

	return result
}

// loadDefaultPatterns loads all PII detection patterns
// NOTE: Order matters! More specific patterns must come before general ones
func (s *Sanitiser) loadDefaultPatterns() {
	s.patterns = []pattern{
		// Passwords in URLs (must come before email pattern)
		{
			regex:       regexp.MustCompile(`://[^:/@\s]+:[^@/\s]+@`),
			replacement: "://[USER]:[PASSWORD]@",
			description: "Passwords in URLs",
		},

		// Email addresses
		{
			regex:       regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
			replacement: "[EMAIL]",
			description: "Email addresses",
		},

		// IPv4 addresses
		{
			regex:       regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
			replacement: "[IP]",
			description: "IPv4 addresses",
		},

		// IPv6 addresses
		{
			regex:       regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),
			replacement: "[IP]",
			description: "IPv6 addresses",
		},

		// UUIDs
		{
			regex:       regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
			replacement: "[ID]",
			description: "UUIDs",
		},

		// Credit card numbers (basic pattern)
		{
			regex:       regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),
			replacement: "[CARD]",
			description: "Credit card numbers",
		},

		// JWT tokens (must come before API keys pattern)
		{
			regex:       regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
			replacement: "[TOKEN]",
			description: "JWT tokens",
		},

		// AWS Access Key ID (must come before API keys)
		{
			regex:       regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
			replacement: "[AWS_KEY]",
			description: "AWS Access Keys",
		},

		// Slack tokens (must come before phone pattern)
		{
			regex:       regexp.MustCompile(`\bxox[baprs]-[0-9a-zA-Z-]+\b`),
			replacement: "[SLACK_TOKEN]",
			description: "Slack tokens",
		},

		// GitHub tokens (must come before API keys)
		{
			regex:       regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`),
			replacement: "[GITHUB_TOKEN]",
			description: "GitHub tokens",
		},

		// Bearer tokens
		{
			regex:       regexp.MustCompile(`\bBearer\s+[A-Za-z0-9._-]+\b`),
			replacement: "Bearer [TOKEN]",
			description: "Bearer tokens",
		},

		// SSH private keys
		{
			regex:       regexp.MustCompile(`-----BEGIN.*PRIVATE KEY-----[\s\S]*?-----END.*PRIVATE KEY-----`),
			replacement: "[SSH_KEY]",
			description: "SSH private keys",
		},

		// Phone numbers (US format) - more precise pattern
		{
			regex:       regexp.MustCompile(`(?:\+1[-.\s]?)?(?:\(?\d{3}\)?[-.\s]?|\d{3}[-.\s])\d{3}[-.\s]\d{4}\b`),
			replacement: "[PHONE]",
			description: "Phone numbers",
		},

		// API keys (common formats: 32-64 hex chars) - must come after more specific patterns
		{
			regex:       regexp.MustCompile(`\b[A-Za-z0-9]{32,64}\b`),
			replacement: "[SECRET]",
			description: "API keys and secrets",
		},

		// Social Security Numbers
		{
			regex:       regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			replacement: "[SSN]",
			description: "Social Security Numbers",
		},

		// AWS ARNs
		{
			regex:       regexp.MustCompile(`\barn:aws:[a-z0-9-]+:[a-z0-9-]*:\d{12}:[^\s]+`),
			replacement: "[RESOURCE]",
			description: "AWS ARNs",
		},

		// GCP resource names
		{
			regex:       regexp.MustCompile(`\bprojects/[a-z0-9-]+/[^\s]+`),
			replacement: "[RESOURCE]",
			description: "GCP resource names",
		},

		// Azure resource IDs
		{
			regex:       regexp.MustCompile(`\b/subscriptions/[a-f0-9-]+/[^\s]+`),
			replacement: "[RESOURCE]",
			description: "Azure resource IDs",
		},

		// Internal hostnames (basic pattern)
		{
			regex:       regexp.MustCompile(`\b[a-z0-9-]+\.internal(?:\.[a-z0-9-]+)*\b`),
			replacement: "[HOST]",
			description: "Internal hostnames",
		},

		// Database connection strings (common formats)
		{
			regex:       regexp.MustCompile(`\b(?:postgres|mysql|mongodb)://[^\s]+`),
			replacement: "[DB_CONNECTION]",
			description: "Database connection strings",
		},
	}
}

// SanitiseMultiple sanitises multiple strings and returns them
func (s *Sanitiser) SanitiseMultiple(texts ...string) []string {
	result := make([]string, len(texts))
	for i, text := range texts {
		result[i] = s.Sanitise(text)
	}
	return result
}

// SanitiseMap sanitises all values in a map
func (s *Sanitiser) SanitiseMap(data map[string]string) map[string]string {
	result := make(map[string]string, len(data))
	for k, v := range data {
		result[k] = s.Sanitise(v)
	}
	return result
}

// GetPatternCount returns the number of PII patterns loaded
func (s *Sanitiser) GetPatternCount() int {
	return len(s.patterns)
}

// IsSafe checks if text contains any PII patterns
func (s *Sanitiser) IsSafe(text string) bool {
	for _, p := range s.patterns {
		if p.regex.MatchString(text) {
			return false
		}
	}
	return true
}
