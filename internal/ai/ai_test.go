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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// roundTripFunc lets us intercept http.Client calls regardless of URL,
// so we can test Claude/OpenAI/Gemini without modifying production code.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockClient(handler http.HandlerFunc) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			handler(w, req)
			return w.Result(), nil
		}),
	}
}

// ── BuildPrompt ───────────────────────────────────────────────────────────────

func TestBuildPrompt_MinimalRequest(t *testing.T) {
	req := &AnalysisRequest{
		IncidentTitle: "High CPU",
		Severity:      "high",
	}
	prompt := BuildPrompt(req)

	if !strings.Contains(prompt, "High CPU") {
		t.Error("prompt should contain incident title")
	}
	if !strings.Contains(prompt, "high") {
		t.Error("prompt should contain severity")
	}
	if !strings.Contains(prompt, "ROOT CAUSE") {
		t.Error("prompt should contain ROOT CAUSE instruction")
	}
}

func TestBuildPrompt_WithAllFields(t *testing.T) {
	req := &AnalysisRequest{
		IncidentTitle:   "Database timeout",
		IncidentMessage: "Connection pool exhausted",
		Severity:        "critical",
		Commits:         []string{"fix: bump connection limit", "feat: add retry logic"},
		ErrorLogs:       []string{"ERROR: connect timeout", "ERROR: pool full"},
		MetricsSummary:  "CPU 95%, Memory 80%",
		Timeline:        "Deploy 5min before alert",
	}
	prompt := BuildPrompt(req)

	for _, want := range []string{
		"Database timeout",
		"Connection pool exhausted",
		"critical",
		"fix: bump connection limit",
		"ERROR: connect timeout",
		"CPU 95%",
		"Deploy 5min before alert",
		"RECENT DEPLOYS",
		"ERROR LOGS",
		"METRICS",
		"TIMELINE",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPrompt_TruncatesLongErrorLogs(t *testing.T) {
	logs := make([]string, 15)
	for i := range logs {
		logs[i] = fmt.Sprintf("ERROR line %d", i)
	}
	req := &AnalysisRequest{
		IncidentTitle: "Test",
		Severity:      "high",
		ErrorLogs:     logs,
	}
	prompt := BuildPrompt(req)

	// Only first 10 are shown inline; rest counted
	if !strings.Contains(prompt, "and 5 more errors") {
		t.Error("expected truncation message for >10 error logs")
	}
}

func TestBuildPrompt_NoCommitsSection(t *testing.T) {
	req := &AnalysisRequest{
		IncidentTitle: "Alert",
		Severity:      "low",
	}
	prompt := BuildPrompt(req)
	if strings.Contains(prompt, "RECENT DEPLOYS") {
		t.Error("prompt should not contain RECENT DEPLOYS when no commits")
	}
}

// ── ParseAnalysisResponse ────────────────────────────────────────────────────

func TestParseAnalysisResponse_WellFormatted(t *testing.T) {
	raw := `ROOT CAUSE
Memory leak in connection pool caused exhaustion after deploy.

SUGGESTED ACTION
Restart the affected pods and roll back the last deploy.

CONFIDENCE
High`

	resp := ParseAnalysisResponse(raw)

	if !strings.Contains(resp.RootCause, "Memory leak") {
		t.Errorf("unexpected RootCause: %q", resp.RootCause)
	}
	if !strings.Contains(resp.SuggestedAction, "Restart") {
		t.Errorf("unexpected SuggestedAction: %q", resp.SuggestedAction)
	}
	if resp.Confidence != "high" {
		t.Errorf("expected confidence=high, got %q", resp.Confidence)
	}
}

func TestParseAnalysisResponse_LowConfidence(t *testing.T) {
	raw := `ROOT CAUSE
Unknown issue.
CONFIDENCE
Low`
	resp := ParseAnalysisResponse(raw)
	if resp.Confidence != "low" {
		t.Errorf("expected confidence=low, got %q", resp.Confidence)
	}
}

func TestParseAnalysisResponse_DefaultConfidence(t *testing.T) {
	raw := `ROOT CAUSE
Something broke.`
	resp := ParseAnalysisResponse(raw)
	if resp.Confidence != "medium" {
		t.Errorf("expected default confidence=medium, got %q", resp.Confidence)
	}
}

func TestParseAnalysisResponse_Fallback(t *testing.T) {
	// No sections detected — entire text goes to RootCause
	raw := "Just a plain response with no sections."
	resp := ParseAnalysisResponse(raw)

	if resp.RootCause != raw {
		t.Errorf("expected fallback to put raw text in RootCause, got %q", resp.RootCause)
	}
	if resp.SuggestedAction == "" {
		t.Error("expected fallback SuggestedAction to be set")
	}
}

func TestParseAnalysisResponse_EmptyString(t *testing.T) {
	resp := ParseAnalysisResponse("")
	if resp == nil {
		t.Fatal("expected non-nil response for empty string")
	}
	if resp.Confidence != "medium" {
		t.Errorf("expected default confidence, got %q", resp.Confidence)
	}
}

// ── OllamaBackend ─────────────────────────────────────────────────────────────

func TestOllamaBackend_GetModelName(t *testing.T) {
	b := NewOllamaBackend("http://localhost:11434", "llama3.2")
	if b.GetModelName() != "llama3.2" {
		t.Errorf("expected llama3.2, got %q", b.GetModelName())
	}
}

func TestOllamaBackend_IsAvailable_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	b := NewOllamaBackend(srv.URL, "llama3.2")
	if !b.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable=true when server responds 200")
	}
}

func TestOllamaBackend_IsAvailable_Failure(t *testing.T) {
	b := NewOllamaBackend("http://127.0.0.1:19999", "llama3.2")
	if b.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable=false for unreachable endpoint")
	}
}

func TestOllamaBackend_Analyze_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
		case "/api/generate":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(OllamaResponse{
				Response: "  Memory leak in connection pool.  ",
				Done:     true,
			})
		}
	}))
	defer srv.Close()

	b := NewOllamaBackend(srv.URL, "llama3.2")
	result, err := b.Analyze(context.Background(), "analyze this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Memory leak in connection pool." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestOllamaBackend_Analyze_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
		case "/api/generate":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "internal error") //nolint:errcheck
		}
	}))
	defer srv.Close()

	b := NewOllamaBackend(srv.URL, "llama3.2")
	_, err := b.Analyze(context.Background(), "analyze this")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestOllamaBackend_Analyze_Unavailable(t *testing.T) {
	b := NewOllamaBackend("http://127.0.0.1:19999", "llama3.2")
	_, err := b.Analyze(context.Background(), "analyze")
	if err == nil {
		t.Error("expected error when Ollama is unavailable")
	}
}

// ── ClaudeBackend ─────────────────────────────────────────────────────────────

func TestClaudeBackend_GetModelName(t *testing.T) {
	b := NewClaudeBackend("key", "claude-sonnet-4-6")
	if b.GetModelName() != "claude-sonnet-4-6" {
		t.Errorf("unexpected model: %q", b.GetModelName())
	}
}

func TestClaudeBackend_DefaultModel(t *testing.T) {
	b := NewClaudeBackend("key", "")
	if b.GetModelName() == "" {
		t.Error("expected default model to be set")
	}
}

func TestClaudeBackend_IsAvailable_WithKey(t *testing.T) {
	b := NewClaudeBackend("sk-ant-test", "")
	if !b.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable=true with API key")
	}
}

func TestClaudeBackend_IsAvailable_NoKey(t *testing.T) {
	b := NewClaudeBackend("", "")
	if b.IsAvailable(context.Background()) {
		t.Error("expected IsAvailable=false with empty API key")
	}
}

func TestClaudeBackend_Analyze_NoKey(t *testing.T) {
	b := NewClaudeBackend("", "")
	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error when no API key")
	}
}

func TestClaudeBackend_Analyze_Success(t *testing.T) {
	b := &ClaudeBackend{
		apiKey: "sk-ant-test",
		model:  "claude-sonnet-4-6",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "sk-ant-test" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(claudeResponse{
				Content: []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{{Type: "text", Text: "  Root cause: deploy.  "}},
			})
		}),
	}

	result, err := b.Analyze(context.Background(), "analyze this")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Root cause: deploy." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestClaudeBackend_Analyze_APIError(t *testing.T) {
	b := &ClaudeBackend{
		apiKey: "sk-ant-test",
		model:  "claude-sonnet-4-6",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid key"}`) //nolint:errcheck
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestClaudeBackend_Analyze_EmptyContent(t *testing.T) {
	b := &ClaudeBackend{
		apiKey: "sk-ant-test",
		model:  "claude-sonnet-4-6",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(claudeResponse{Content: nil})
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for empty content response")
	}
}

// ── OpenAIBackend ─────────────────────────────────────────────────────────────

func TestOpenAIBackend_GetModelName(t *testing.T) {
	b := NewOpenAIBackend("key", "gpt-4o")
	if b.GetModelName() != "gpt-4o" {
		t.Errorf("unexpected model: %q", b.GetModelName())
	}
}

func TestOpenAIBackend_DefaultModel(t *testing.T) {
	b := NewOpenAIBackend("key", "")
	if b.GetModelName() == "" {
		t.Error("expected default model")
	}
}

func TestOpenAIBackend_IsAvailable(t *testing.T) {
	if !NewOpenAIBackend("key", "").IsAvailable(context.Background()) {
		t.Error("expected true with key")
	}
	if NewOpenAIBackend("", "").IsAvailable(context.Background()) {
		t.Error("expected false without key")
	}
}

func TestOpenAIBackend_Analyze_NoKey(t *testing.T) {
	b := NewOpenAIBackend("", "")
	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error when no API key")
	}
}

func TestOpenAIBackend_Analyze_Success(t *testing.T) {
	b := &OpenAIBackend{
		apiKey: "sk-test",
		model:  "gpt-4o-mini",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(OpenAIResponse{
				Choices: []struct {
					Index   int `json:"index"`
					Message struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				}{{Message: struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}{Role: "assistant", Content: "  Deploy caused the issue.  "}}},
			})
		}),
	}

	result, err := b.Analyze(context.Background(), "analyze")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Deploy caused the issue." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestOpenAIBackend_Analyze_EmptyChoices(t *testing.T) {
	b := &OpenAIBackend{
		apiKey: "sk-test",
		model:  "gpt-4o-mini",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(OpenAIResponse{Choices: nil})
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestOpenAIBackend_Analyze_APIError(t *testing.T) {
	b := &OpenAIBackend{
		apiKey: "sk-test",
		model:  "gpt-4o-mini",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"rate limit"}`) //nolint:errcheck
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for 429 response")
	}
}

// ── GeminiBackend ─────────────────────────────────────────────────────────────

func TestGeminiBackend_GetModelName(t *testing.T) {
	b := NewGeminiBackend("key", "gemini-2.0-flash")
	if b.GetModelName() != "gemini-2.0-flash" {
		t.Errorf("unexpected model: %q", b.GetModelName())
	}
}

func TestGeminiBackend_DefaultModel(t *testing.T) {
	b := NewGeminiBackend("key", "")
	if b.GetModelName() == "" {
		t.Error("expected default model")
	}
}

func TestGeminiBackend_IsAvailable(t *testing.T) {
	if !NewGeminiBackend("key", "").IsAvailable(context.Background()) {
		t.Error("expected true with key")
	}
	if NewGeminiBackend("", "").IsAvailable(context.Background()) {
		t.Error("expected false without key")
	}
}

func TestGeminiBackend_Analyze_NoKey(t *testing.T) {
	b := NewGeminiBackend("", "")
	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error when no API key")
	}
}

func TestGeminiBackend_Analyze_Success(t *testing.T) {
	b := &GeminiBackend{
		apiKey: "test-key",
		model:  "gemini-2.0-flash",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(geminiResponse{
				Candidates: []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				}{{Content: struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				}{Parts: []struct {
					Text string `json:"text"`
				}{{Text: "  Memory leak detected.  "}}}}},
			})
		}),
	}

	result, err := b.Analyze(context.Background(), "analyze")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Memory leak detected." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestGeminiBackend_Analyze_APIError(t *testing.T) {
	b := &GeminiBackend{
		apiKey: "test-key",
		model:  "gemini-2.0-flash",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"message":"API key invalid","code":403}}`)
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestGeminiBackend_Analyze_EmptyCandidates(t *testing.T) {
	b := &GeminiBackend{
		apiKey: "test-key",
		model:  "gemini-2.0-flash",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(geminiResponse{Candidates: nil})
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

func TestGeminiBackend_Analyze_ErrorField(t *testing.T) {
	b := &GeminiBackend{
		apiKey: "test-key",
		model:  "gemini-2.0-flash",
		client: mockClient(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(geminiResponse{
				Error: &struct {
					Message string `json:"message"`
					Code    int    `json:"code"`
				}{Message: "quota exceeded", Code: 429},
			})
		}),
	}

	_, err := b.Analyze(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error when response contains error field")
	}
	if !strings.Contains(err.Error(), "quota exceeded") {
		t.Errorf("expected error message to contain quota text, got: %v", err)
	}
}
