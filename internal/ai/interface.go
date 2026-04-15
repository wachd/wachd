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

package ai

import (
	"context"
)

// Backend represents an AI backend for root cause analysis
type Backend interface {
	// Analyze performs root cause analysis on the given prompt
	// Returns plain-English analysis or error
	Analyze(ctx context.Context, prompt string) (string, error)

	// GetModelName returns the name of the AI model being used
	GetModelName() string

	// IsAvailable checks if the AI backend is available
	IsAvailable(ctx context.Context) bool
}

// AnalysisRequest represents a request for incident analysis
type AnalysisRequest struct {
	IncidentTitle   string
	IncidentMessage string
	Severity        string
	Commits         []string // Sanitized commit messages
	ErrorLogs       []string // Sanitized error log lines
	MetricsSummary  string   // Summary of metric anomalies
	Timeline        string   // Correlation timeline summary
}

// AnalysisResponse represents the AI's analysis response
type AnalysisResponse struct {
	RootCause       string   `json:"root_cause"`        // 1-2 sentence explanation
	SuggestedAction string   `json:"suggested_action"`  // What to do next
	Confidence      string   `json:"confidence"`        // high, medium, low
	KeyFindings     []string `json:"key_findings"`      // Bullet points
}
