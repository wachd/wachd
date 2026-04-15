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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaBackend implements AI backend using Ollama
type OllamaBackend struct {
	endpoint string
	model    string
	client   *http.Client
}

// OllamaRequest represents a request to Ollama API
type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// OllamaResponse represents a response from Ollama API
type OllamaResponse struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Response  string    `json:"response"`
	Done      bool      `json:"done"`
}

// NewOllamaBackend creates a new Ollama backend
func NewOllamaBackend(endpoint, model string) *OllamaBackend {
	return &OllamaBackend{
		endpoint: endpoint,
		model:    model,
		client: &http.Client{
			Timeout: 120 * time.Second, // AI calls can take time
		},
	}
}

// Analyze performs root cause analysis using Ollama
func (o *OllamaBackend) Analyze(ctx context.Context, prompt string) (string, error) {
	if !o.IsAvailable(ctx) {
		return "", fmt.Errorf("ollama backend is not available at %s", o.endpoint)
	}

	req := OllamaRequest{
		Model:  o.model,
		Prompt: prompt,
		Stream: false,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/generate", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var ollamaResp OllamaResponse
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return strings.TrimSpace(ollamaResp.Response), nil
}

// GetModelName returns the model name
func (o *OllamaBackend) GetModelName() string {
	return o.model
}

// IsAvailable checks if Ollama is reachable
func (o *OllamaBackend) IsAvailable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", o.endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK
}

// BuildPrompt builds a structured prompt for incident analysis
func BuildPrompt(req *AnalysisRequest) string {
	var prompt strings.Builder

	prompt.WriteString("You are a Site Reliability Engineering (SRE) assistant analyzing a production incident.\n\n")

	prompt.WriteString("INCIDENT DETAILS:\n")
	fmt.Fprintf(&prompt, "Title: %s\n", req.IncidentTitle)
	if req.IncidentMessage != "" {
		fmt.Fprintf(&prompt, "Message: %s\n", req.IncidentMessage)
	}
	fmt.Fprintf(&prompt, "Severity: %s\n\n", req.Severity)

	if len(req.Commits) > 0 {
		prompt.WriteString("RECENT DEPLOYS (last 30 min):\n")
		for _, commit := range req.Commits {
			fmt.Fprintf(&prompt, "- %s\n", commit)
		}
		prompt.WriteString("\n")
	}

	if len(req.ErrorLogs) > 0 {
		prompt.WriteString("ERROR LOGS (sanitized):\n")
		for i, log := range req.ErrorLogs {
			if i < 10 {
				fmt.Fprintf(&prompt, "- %s\n", log)
			}
		}
		if len(req.ErrorLogs) > 10 {
			fmt.Fprintf(&prompt, "... and %d more errors\n", len(req.ErrorLogs)-10)
		}
		prompt.WriteString("\n")
	}

	if req.MetricsSummary != "" {
		fmt.Fprintf(&prompt, "METRICS: %s\n\n", req.MetricsSummary)
	}

	if req.Timeline != "" {
		fmt.Fprintf(&prompt, "TIMELINE: %s\n\n", req.Timeline)
	}

	prompt.WriteString("TASK:\n")
	prompt.WriteString("Analyze this incident and provide:\n")
	prompt.WriteString("1. ROOT CAUSE: Most likely cause (1-2 sentences)\n")
	prompt.WriteString("2. SUGGESTED ACTION: Specific next step to take\n")
	prompt.WriteString("3. CONFIDENCE: High, Medium, or Low\n\n")
	prompt.WriteString("Be concise and actionable. Focus on what the on-call engineer should do NOW.\n")

	return prompt.String()
}

// ParseAnalysisResponse attempts to parse the AI response into structured format
func ParseAnalysisResponse(rawResponse string) *AnalysisResponse {
	response := &AnalysisResponse{
		Confidence:  "medium",
		KeyFindings: []string{},
	}

	lines := strings.Split(rawResponse, "\n")
	currentSection := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lowerLine := strings.ToLower(line)

		// Detect sections
		if strings.Contains(lowerLine, "root cause") {
			currentSection = "root_cause"
			continue
		} else if strings.Contains(lowerLine, "suggested action") || strings.Contains(lowerLine, "action") {
			currentSection = "action"
			continue
		} else if strings.Contains(lowerLine, "confidence") {
			currentSection = "confidence"
			continue
		}

		// Parse content
		switch currentSection {
		case "root_cause":
			if response.RootCause == "" {
				response.RootCause = line
			} else {
				response.RootCause += " " + line
			}
		case "action":
			if response.SuggestedAction == "" {
				response.SuggestedAction = line
			} else {
				response.SuggestedAction += " " + line
			}
		case "confidence":
			if strings.Contains(lowerLine, "high") {
				response.Confidence = "high"
			} else if strings.Contains(lowerLine, "low") {
				response.Confidence = "low"
			}
		}
	}

	// Fallback: if parsing failed, put entire response in root cause
	if response.RootCause == "" {
		response.RootCause = rawResponse
		response.SuggestedAction = "Review incident details and determine appropriate action"
	}

	return response
}
