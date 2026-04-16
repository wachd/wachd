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

// GeminiBackend implements the AI Backend interface using Google Gemini API.
type GeminiBackend struct {
	apiKey string
	model  string
	client *http.Client
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

// NewGeminiBackend creates a new Google Gemini backend.
// model defaults to "gemini-2.0-flash" — fast and free-tier eligible.
func NewGeminiBackend(apiKey, model string) *GeminiBackend {
	if model == "" {
		model = "gemini-2.0-flash"
	}
	return &GeminiBackend{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Analyze performs root cause analysis using Gemini.
func (g *GeminiBackend) Analyze(ctx context.Context, prompt string) (string, error) {
	if !g.IsAvailable(ctx) {
		return "", fmt.Errorf("gemini: API key not configured")
	}

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		g.model, g.apiKey,
	)

	body := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gemini: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: API returned %d: %s", resp.StatusCode, string(b))
	}

	var gr geminiResponse
	if err := json.Unmarshal(b, &gr); err != nil {
		return "", fmt.Errorf("gemini: parse response: %w", err)
	}

	if gr.Error != nil {
		return "", fmt.Errorf("gemini: %s", gr.Error.Message)
	}

	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini: empty response")
	}

	return strings.TrimSpace(gr.Candidates[0].Content.Parts[0].Text), nil
}

// GetModelName returns the configured model name.
func (g *GeminiBackend) GetModelName() string {
	return g.model
}

// IsAvailable returns true if an API key is configured.
func (g *GeminiBackend) IsAvailable(_ context.Context) bool {
	return g.apiKey != ""
}
