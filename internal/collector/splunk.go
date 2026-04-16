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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SplunkCollector fetches events and notable events from Splunk via the REST API.
// Requires a Splunk token (or username/password) with search privileges.
// Supports both Splunk Enterprise and Splunk Cloud.
type SplunkCollector struct {
	endpoint string // e.g. https://splunk.company.com:8089
	token    string // Bearer token or base64(user:pass) for basic auth
	authType string // "bearer" | "basic"
	client   *http.Client
}

// SplunkEvent represents a single Splunk search result event.
type SplunkEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	Raw       string            `json:"raw"`
	Source    string            `json:"source"`
	Host      string            `json:"host"`
	Fields    map[string]string `json:"fields"`
}

// NewSplunkCollector creates a Splunk collector using a bearer token (recommended).
// endpoint: Splunk management port URL, e.g. https://splunk.company.com:8089
// token: Splunk authentication token
func NewSplunkCollector(endpoint, token string) *SplunkCollector {
	return &SplunkCollector{
		endpoint: strings.TrimRight(endpoint, "/"),
		token:    token,
		authType: "bearer",
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// NewSplunkCollectorBasicAuth creates a Splunk collector using username/password.
func NewSplunkCollectorBasicAuth(endpoint, username, password string) *SplunkCollector {
	return &SplunkCollector{
		endpoint: strings.TrimRight(endpoint, "/"),
		token:    username + ":" + password,
		authType: "basic",
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// FetchLogs runs an SPL search scoped to a service and time window and returns
// at most limit matching events. Only ERROR/WARN level events are returned.
func (s *SplunkCollector) FetchLogs(ctx context.Context, service string, since, until time.Time, limit int) ([]LogLine, error) {
	if s.endpoint == "" || s.token == "" {
		return nil, fmt.Errorf("splunk endpoint or token not configured")
	}

	spl := fmt.Sprintf(
		`search index=* (service="%s" OR sourcetype="%s" OR host="%s") `+
			`(level=ERROR OR level=WARN OR level=CRITICAL OR severity=error OR severity=warn) `+
			`earliest=%d latest=%d | head %d`,
		service, service, service,
		since.Unix(), until.Unix(),
		limit,
	)

	events, err := s.runSearch(ctx, spl)
	if err != nil {
		return nil, err
	}

	lines := make([]LogLine, 0, len(events))
	for _, e := range events {
		level := e.Fields["level"]
		if level == "" {
			level = e.Fields["severity"]
		}
		lines = append(lines, LogLine{
			Timestamp: e.Timestamp,
			Message:   e.Raw,
			Level:     level,
			Labels:    e.Fields,
		})
	}
	return lines, nil
}

// FetchNotableEvents fetches Splunk ITSI / ES notable events related to a service.
// Returns correlated notable events that Splunk has already grouped — useful context
// for AI analysis since Splunk has already done first-pass correlation.
func (s *SplunkCollector) FetchNotableEvents(ctx context.Context, service string, since time.Time, limit int) ([]SplunkEvent, error) {
	if s.endpoint == "" || s.token == "" {
		return nil, fmt.Errorf("splunk endpoint or token not configured")
	}

	spl := fmt.Sprintf(
		`search index=notable (service="%s" OR orig_service="%s") `+
			`earliest=%d | head %d`,
		service, service,
		since.Unix(),
		limit,
	)

	return s.runSearch(ctx, spl)
}

// runSearch submits a blocking Splunk search job and returns the results.
// Uses the oneshot search endpoint for simplicity — suitable for short time windows.
func (s *SplunkCollector) runSearch(ctx context.Context, spl string) ([]SplunkEvent, error) {
	form := url.Values{}
	form.Set("search", spl)
	form.Set("output_mode", "json")
	form.Set("count", "100")
	form.Set("exec_mode", "oneshot") // blocking — returns results directly

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.endpoint+"/services/search/jobs",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("splunk search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("splunk returned %d: %s", resp.StatusCode, string(body))
	}

	// Splunk oneshot response: {"results": [...], "fields": [...]}
	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}

	events := make([]SplunkEvent, 0, len(result.Results))
	for _, r := range result.Results {
		ev := SplunkEvent{
			Fields: make(map[string]string),
		}

		// Parse _time field
		if ts, ok := r["_time"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				ev.Timestamp = t
			}
		}

		// _raw is the full original log line
		if raw, ok := r["_raw"].(string); ok {
			ev.Raw = raw
		}
		if src, ok := r["source"].(string); ok {
			ev.Source = src
		}
		if host, ok := r["host"].(string); ok {
			ev.Host = host
		}

		// Collect all string fields as labels (skip internal _ fields)
		for k, v := range r {
			if strings.HasPrefix(k, "_") {
				continue
			}
			if str, ok := v.(string); ok {
				ev.Fields[k] = str
			}
		}

		events = append(events, ev)
	}
	return events, nil
}

// setAuth adds the appropriate Authorization header to the request.
func (s *SplunkCollector) setAuth(req *http.Request) {
	switch s.authType {
	case "basic":
		// token is "user:pass"
		parts := strings.SplitN(s.token, ":", 2)
		if len(parts) == 2 {
			req.SetBasicAuth(parts[0], parts[1])
		}
	default:
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
}
