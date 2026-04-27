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
	"strconv"
	"time"
)

// LogsCollector fetches logs from Loki
type LogsCollector struct {
	endpoint string
	client   *http.Client
}

// LogLine represents a single log line
type LogLine struct {
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Level     string            `json:"level"`
	Labels    map[string]string `json:"labels"`
}

// LokiResponse represents the Loki API response
type LokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [timestamp_ns, log_line]
		} `json:"result"`
	} `json:"data"`
}

// NewLogsCollector creates a new Loki logs collector
func NewLogsCollector(endpoint string) *LogsCollector {
	return &LogsCollector{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchLogs fetches logs from Loki
// query: LogQL query, e.g., `{service="api"} |= "error"`
func (l *LogsCollector) FetchLogs(ctx context.Context, query string, since, until time.Time, limit int) ([]LogLine, error) {
	if l.endpoint == "" {
		return nil, fmt.Errorf("loki endpoint not configured")
	}

	// Build query URL
	queryURL := fmt.Sprintf("%s/loki/api/v1/query_range", l.endpoint)

	params := url.Values{}
	params.Add("query", query)
	params.Add("start", fmt.Sprintf("%d", since.UnixNano()))
	params.Add("end", fmt.Sprintf("%d", until.UnixNano()))
	params.Add("limit", fmt.Sprintf("%d", limit))

	fullURL := fmt.Sprintf("%s?%s", queryURL, params.Encode())

	// Make request
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("loki returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var lokiResp LokiResponse
	if err := json.NewDecoder(resp.Body).Decode(&lokiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to LogLine
	var logs []LogLine
	for _, result := range lokiResp.Data.Result {
		for _, value := range result.Values {
			if len(value) < 2 {
				continue
			}

			// Parse timestamp — Loki sends Unix nanoseconds as a string.
			// Keep RFC3339Nano as a fallback for test doubles and non-standard setups.
			// Skip the entry entirely if neither format parses — never substitute time.Now().
			var ts time.Time
			if ns, err := strconv.ParseInt(value[0], 10, 64); err == nil {
				ts = time.Unix(0, ns).UTC()
			} else if t, err := time.Parse(time.RFC3339Nano, value[0]); err == nil {
				ts = t
			} else {
				continue
			}

			logLine := LogLine{
				Timestamp: ts,
				Message:   value[1],
				Labels:    result.Stream,
				Level:     result.Stream["level"], // Common label
			}

			logs = append(logs, logLine)
		}
	}

	return logs, nil
}

// FetchErrorLogs fetches only ERROR and CRITICAL level logs
func (l *LogsCollector) FetchErrorLogs(ctx context.Context, service string, since, until time.Time, limit int) ([]LogLine, error) {
	// Build LogQL query for error logs
	query := fmt.Sprintf(`{service="%s"} |~ "(?i)(error|critical|fatal)"`, service)
	return l.FetchLogs(ctx, query, since, until, limit)
}
