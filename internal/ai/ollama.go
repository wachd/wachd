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
		prompt.WriteString("RECENT DEPLOYS AND CONFIG CHANGES (newest first; compare each commit time with the alert time):\n")
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
	prompt.WriteString("3. CONFIDENCE: High, Medium, or Low\n")
	prompt.WriteString("4. DEPLOYMENT CAUSE: YES if a recent deployment listed above is the probable root cause, NO otherwise\n\n")
	prompt.WriteString("5. EVIDENCE CLASSIFICATION: CONFIRMED_ROOT_CAUSE, PROBABLE_ROOT_CAUSE, TEMPORAL_CORRELATION_ONLY, or INSUFFICIENT_EVIDENCE\n\n")
	prompt.WriteString("Only call a configuration change confirmed when the retrieved commit patch explicitly shows it and telemetry supports the mechanism. Never invent file changes, values, or causal claims.\n\n")
	prompt.WriteString("Cite your evidence: name the commit SHA and the exact changed setting with old and new values from the patch, the metric names with their values, and the log event or message you relied on. If one of these is missing, say it is missing instead of inferring it. Prefer the most recent commit before the alert that changes the relevant setting.\n\n")
	prompt.WriteString("Be concise and actionable. Focus on what the on-call engineer should do NOW.\n")

	return prompt.String()
}

// analysisSections maps a section label to its key. A line starts a section
// only when, after stripping markdown, it begins with one of these labels.
var analysisSections = []struct{ label, key string }{
	{"evidence classification", "evidence_class"},
	{"evidence class", "evidence_class"},
	{"deployment cause", "deployment_cause"},
	{"suggested action", "action"},
	{"root cause", "root_cause"},
	{"confidence", "confidence"},
	{"key findings", "findings"},
}

// matchSectionHeading reports the section a line introduces and any content
// that follows the label on the same line. It accepts markdown headings,
// numbering and bold ("### 1. ROOT CAUSE", "**Confidence:** High") but never
// matches a label that merely appears inside a sentence.
func matchSectionHeading(line string) (key, rest string, ok bool) {
	t := strings.TrimLeft(line, "#*_ \t>")
	t = strings.TrimLeft(t, "0123456789")
	if len(t) < len(strings.TrimLeft(line, "#*_ \t>")) {
		t = strings.TrimLeft(t, ".) \t*_")
	}
	lower := strings.ToLower(t)
	for _, sec := range analysisSections {
		if !strings.HasPrefix(lower, sec.label) {
			continue
		}
		tail := strings.TrimLeft(t[len(sec.label):], "*_ \t")
		if tail != "" && !strings.ContainsRune(":-–—", rune(tail[0])) && !strings.HasPrefix(tail, "–") && !strings.HasPrefix(tail, "—") {
			// "root cause analysis ..." or "confidence in ..." is prose, not a heading.
			if !strings.HasPrefix(lower[len(sec.label):], ":") {
				continue
			}
		}
		tail = strings.TrimLeft(tail, ":-–— \t*_")
		return sec.key, strings.TrimSpace(tail), true
	}
	return "", "", false
}

// isRule reports whether a line is only a markdown horizontal rule.
func isRule(line string) bool {
	t := strings.Trim(line, " -*_=")
	return t == "" && len(line) >= 3
}

// ParseAnalysisResponse attempts to parse the AI response into structured format
func ParseAnalysisResponse(rawResponse string) *AnalysisResponse {
	response := &AnalysisResponse{
		Confidence:    "medium",
		KeyFindings:   []string{},
		EvidenceClass: "INSUFFICIENT_EVIDENCE",
	}

	section := ""
	seen := map[string]bool{}

	add := func(key, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		lower := strings.ToLower(text)
		first := !seen[key]
		seen[key] = true
		switch key {
		case "root_cause":
			if response.RootCause == "" {
				response.RootCause = text
			} else {
				response.RootCause += " " + text
			}
		case "action":
			if response.SuggestedAction == "" {
				response.SuggestedAction = text
			} else {
				response.SuggestedAction += " " + text
			}
		case "confidence":
			if first {
				if strings.Contains(lower, "high") {
					response.Confidence = "high"
				} else if strings.Contains(lower, "low") {
					response.Confidence = "low"
				}
			} else if b := strings.TrimLeft(text, "-*• "); b != text {
				response.KeyFindings = append(response.KeyFindings, strings.TrimSpace(b))
			}
		case "findings":
			response.KeyFindings = append(response.KeyFindings, strings.TrimSpace(strings.TrimLeft(text, "-*• ")))
		case "deployment_cause":
			if first {
				// The answer is the first word (YES/NO); explanations may contain either.
				answer := strings.ToLower(strings.Trim(strings.Fields(lower + " ")[0], "*_.:,;"))
				response.IsDeploymentCause = answer == "yes"
			}
			// Keep the reasoning (for example "no commit was provided") so what the
			// model could not establish is not lost.
			note := strings.TrimLeft(text, "*_ ")
			if f := strings.Fields(note); len(f) > 0 {
				if w := strings.ToLower(strings.Trim(f[0], "*_.:,;")); w == "yes" || w == "no" {
					note = strings.TrimLeft(strings.TrimPrefix(note, f[0]), "*_ -–—:")
				}
			}
			if len(strings.Fields(note)) > 2 {
				response.KeyFindings = append(response.KeyFindings, "Deployment cause: "+note)
			}
		case "evidence_class":
			// The first class named is the answer; later ones are usually
			// contrasts such as "PROBABLE_ROOT_CAUSE, not CONFIRMED_ROOT_CAUSE".
			upper, best := strings.ToUpper(text), -1
			for _, class := range []string{"CONFIRMED_ROOT_CAUSE", "PROBABLE_ROOT_CAUSE", "TEMPORAL_CORRELATION_ONLY", "INSUFFICIENT_EVIDENCE"} {
				if i := strings.Index(upper, class); i >= 0 && (best < 0 || i < best) {
					best, response.EvidenceClass = i, class
				}
			}
		}
	}

	for _, line := range strings.Split(rawResponse, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isRule(line) {
			continue
		}
		if key, rest, ok := matchSectionHeading(line); ok {
			section = key
			add(section, rest)
			continue
		}
		add(section, line)
	}

	// Fallback: if parsing failed, put entire response in root cause
	if response.RootCause == "" {
		response.RootCause = rawResponse
		response.SuggestedAction = "Review incident details and determine appropriate action"
	}

	return response
}
