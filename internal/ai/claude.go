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

// ClaudeBackend implements the AI Backend interface using the Anthropic Messages API.
type ClaudeBackend struct {
	apiKey string
	model  string
	client *http.Client
}

// claudeRequest is the request body for POST /v1/messages.
type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the response from POST /v1/messages.
type claudeResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

// NewClaudeBackend creates a new Anthropic Claude backend.
// model should be a current Claude model ID, e.g. "claude-sonnet-4-6".
func NewClaudeBackend(apiKey, model string) *ClaudeBackend {
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	return &ClaudeBackend{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Analyze performs root cause analysis using Claude.
func (c *ClaudeBackend) Analyze(ctx context.Context, prompt string) (string, error) {
	if !c.IsAvailable(ctx) {
		return "", fmt.Errorf("claude: API key not configured")
	}

	body := claudeRequest{
		Model:     c.model,
		MaxTokens: 1024,
		Messages: []claudeMessage{
			{Role: "user", Content: prompt},
		},
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("claude: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("claude: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("claude: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("claude: API returned %d: %s", resp.StatusCode, string(b))
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("claude: read response: %w", err)
	}

	var cr claudeResponse
	if err := json.Unmarshal(b, &cr); err != nil {
		return "", fmt.Errorf("claude: parse response: %w", err)
	}

	if len(cr.Content) == 0 {
		return "", fmt.Errorf("claude: empty response")
	}

	return strings.TrimSpace(cr.Content[0].Text), nil
}

// GetModelName returns the configured model name.
func (c *ClaudeBackend) GetModelName() string {
	return c.model
}

// IsAvailable returns true if an API key is configured.
func (c *ClaudeBackend) IsAvailable(_ context.Context) bool {
	return c.apiKey != ""
}
