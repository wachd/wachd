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
	"github.com/wachd/wachd/internal/safehttp"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultLokiLabelKey is the stream label used to select a service's logs when
// a team has not configured another one.
const DefaultLokiLabelKey = "service"

// maxLokiLabelKeyLen bounds the configured key.
const maxLokiLabelKeyLen = 253

// lokiLabelName is the grammar of a Loki/LogQL label name. Dots, slashes and
// dashes are not valid in an indexed Loki stream label, so keys such as
// "app.kubernetes.io/name" are rejected rather than guessed at: use the name the
// label actually has in Loki (typically "app_kubernetes_io_name").
var lokiLabelName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateLokiLabelKey returns the Loki stream label to use for a configured
// key, or an error if it is not a valid Loki label name. An empty key means the
// default, "service".
func ValidateLokiLabelKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return DefaultLokiLabelKey, nil
	}
	if len(key) > maxLokiLabelKeyLen {
		return "", fmt.Errorf("loki label key too long (max %d characters)", maxLokiLabelKeyLen)
	}
	if !lokiLabelName.MatchString(key) {
		hint := ""
		if strings.ContainsAny(key, "./-") {
			hint = "; dots, slashes and dashes are not valid in a Loki label name, use the label as Loki names it (for example app_kubernetes_io_name)"
		}
		return "", fmt.Errorf("invalid loki label key %q: must match [A-Za-z_][A-Za-z0-9_]*%s", key, hint)
	}
	return key, nil
}

// LokiStreamSelector builds `{<label>="<value>"}` for one service. The label key
// is validated and the value is quoted and escaped, so neither can alter the
// structure of the query.
func LokiStreamSelector(labelKey, value string) (string, error) {
	name, err := ValidateLokiLabelKey(labelKey)
	if err != nil {
		return "", err
	}
	return "{" + name + "=" + strconv.Quote(value) + "}", nil
}

// errorLogsQuery is the LogQL used for the error-log lookup.
func errorLogsQuery(labelKey, service string) (string, error) {
	sel, err := LokiStreamSelector(labelKey, service)
	if err != nil {
		return "", err
	}
	return sel + ` |~ "(?i)(error|critical|fatal)"`, nil
}

// LogsCollector fetches logs from Loki
type LogsCollector struct {
	endpoint string
	client   *http.Client
	labelKey string // stream label identifying the service; "" means "service"
}

// WithLabelKey sets the stream label used to select a service's logs and
// returns the collector. An empty key keeps the default, "service".
func (l *LogsCollector) WithLabelKey(key string) *LogsCollector {
	l.labelKey = key
	return l
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

// NewLogsCollector creates a new Loki logs collector.
func NewLogsCollector(endpoint string) *LogsCollector {
	return newLogsCollectorWithClient(endpoint, safehttp.CollectorClient(30*time.Second))
}

func newLogsCollectorWithClient(endpoint string, client *http.Client) *LogsCollector {
	if client == nil {
		client = safehttp.CollectorClient(30 * time.Second)
	}

	return &LogsCollector{
		endpoint: endpoint,
		client:   client,
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
	// A bad configured key is an error, never a silent fallback: falling back to
	// "service" would reproduce the empty-logs symptom this setting exists to fix.
	query, err := errorLogsQuery(l.labelKey, service)
	if err != nil {
		return nil, err
	}
	return l.FetchLogs(ctx, query, since, until, limit)
}
