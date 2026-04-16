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
	"time"
)

// DynatraceCollector fetches problems, logs, and metrics from Dynatrace.
// Requires a Dynatrace API token with scopes:
//   - problems.read
//   - logs.read
//   - metrics.read
//   - entities.read
type DynatraceCollector struct {
	endpoint string // e.g. https://abc12345.live.dynatrace.com
	token    string
	client   *http.Client
}

// DytraceProblem represents a Dynatrace problem/anomaly.
type DytraceProblem struct {
	ID           string    `json:"problemId"`
	Title        string    `json:"title"`
	Status       string    `json:"status"` // OPEN | CLOSED
	Severity     string    `json:"severityLevel"`
	StartTime    time.Time `json:"startTime"`
	AffectedEntities []string `json:"affectedEntityNames"`
}

// NewDynatraceCollector creates a Dynatrace collector.
// endpoint: your environment URL, e.g. https://abc12345.live.dynatrace.com
// token: API token with problems.read, logs.read, metrics.read, entities.read
func NewDynatraceCollector(endpoint, token string) *DynatraceCollector {
	return &DynatraceCollector{
		endpoint: endpoint,
		token:    token,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// FetchProblems fetches open problems related to a service in the given time window.
// Returns at most limit problems, newest first.
func (d *DynatraceCollector) FetchProblems(ctx context.Context, service string, since time.Time, limit int) ([]DytraceProblem, error) {
	if d.endpoint == "" || d.token == "" {
		return nil, fmt.Errorf("dynatrace endpoint or token not configured")
	}

	params := url.Values{}
	params.Set("from", fmt.Sprintf("%d", since.UnixMilli()))
	params.Set("pageSize", fmt.Sprintf("%d", limit))
	params.Set("entitySelector", fmt.Sprintf(`type(SERVICE),entityName("%s")`, service))
	params.Set("fields", "problemId,title,status,severityLevel,startTime,affectedEntities")

	body, err := d.get(ctx, "/api/v2/problems?"+params.Encode())
	if err != nil {
		return nil, err
	}

	var resp struct {
		Problems []struct {
			ProblemID        string `json:"problemId"`
			Title            string `json:"title"`
			Status           string `json:"status"`
			SeverityLevel    string `json:"severityLevel"`
			StartTime        int64  `json:"startTime"` // epoch ms
			AffectedEntities []struct {
				Name string `json:"name"`
			} `json:"affectedEntities"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode problems: %w", err)
	}

	problems := make([]DytraceProblem, 0, len(resp.Problems))
	for _, p := range resp.Problems {
		names := make([]string, 0, len(p.AffectedEntities))
		for _, e := range p.AffectedEntities {
			names = append(names, e.Name)
		}
		problems = append(problems, DytraceProblem{
			ID:               p.ProblemID,
			Title:            p.Title,
			Status:           p.Status,
			Severity:         p.SeverityLevel,
			StartTime:        time.UnixMilli(p.StartTime),
			AffectedEntities: names,
		})
	}
	return problems, nil
}

// FetchLogs fetches error logs for a service from the Dynatrace Logs API v2.
// Returns at most limit log lines within the time window.
func (d *DynatraceCollector) FetchLogs(ctx context.Context, service string, since, until time.Time, limit int) ([]LogLine, error) {
	if d.endpoint == "" || d.token == "" {
		return nil, fmt.Errorf("dynatrace endpoint or token not configured")
	}

	params := url.Values{}
	params.Set("from", fmt.Sprintf("%d", since.UnixMilli()))
	params.Set("to", fmt.Sprintf("%d", until.UnixMilli()))
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("query", fmt.Sprintf(`status:(ERROR OR CRITICAL OR WARN) AND dt.entity.service.name:"%s"`, service))
	params.Set("sort", "-timestamp")

	body, err := d.get(ctx, "/api/v2/logs/search?"+params.Encode())
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results []struct {
			Timestamp  int64             `json:"timestamp"` // epoch ms
			Content    string            `json:"content"`
			Status     string            `json:"status"`
			Attributes map[string]string `json:"attributes"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode logs: %w", err)
	}

	lines := make([]LogLine, 0, len(resp.Results))
	for _, r := range resp.Results {
		labels := r.Attributes
		if labels == nil {
			labels = map[string]string{}
		}
		labels["service"] = service
		lines = append(lines, LogLine{
			Timestamp: time.UnixMilli(r.Timestamp),
			Message:   r.Content,
			Level:     r.Status,
			Labels:    labels,
		})
	}
	return lines, nil
}

// FetchMetrics fetches a metric time series from Dynatrace Metrics API v2.
// metricSelector: Dynatrace metric key, e.g. "ext:app.error_rate" or "builtin:service.errors.total.rate"
func (d *DynatraceCollector) FetchMetrics(ctx context.Context, service, metricSelector string, since, until time.Time) ([]MetricPoint, error) {
	if d.endpoint == "" || d.token == "" {
		return nil, fmt.Errorf("dynatrace endpoint or token not configured")
	}

	params := url.Values{}
	params.Set("metricSelector", metricSelector)
	params.Set("from", fmt.Sprintf("%d", since.UnixMilli()))
	params.Set("to", fmt.Sprintf("%d", until.UnixMilli()))
	params.Set("entitySelector", fmt.Sprintf(`type(SERVICE),entityName("%s")`, service))
	params.Set("resolution", "1m")

	body, err := d.get(ctx, "/api/v2/metrics/query?"+params.Encode())
	if err != nil {
		return nil, err
	}

	var resp struct {
		Resolution string `json:"resolution"`
		Result     []struct {
			MetricID string `json:"metricId"`
			Data     []struct {
				Timestamps []int64   `json:"timestamps"` // epoch ms
				Values     []float64 `json:"values"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}

	var points []MetricPoint
	for _, r := range resp.Result {
		for _, series := range r.Data {
			for i, ts := range series.Timestamps {
				if i >= len(series.Values) {
					break
				}
				points = append(points, MetricPoint{
					Timestamp: time.UnixMilli(ts),
					Value:     series.Values[i],
					Labels:    map[string]string{"metric": r.MetricID, "service": service},
				})
			}
		}
	}
	return points, nil
}

// get makes an authenticated GET request to the Dynatrace API.
func (d *DynatraceCollector) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.endpoint+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Api-Token "+d.token)
	req.Header.Set("Accept", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dynatrace request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dynatrace returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
