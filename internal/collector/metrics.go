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
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// MetricsCollector fetches metrics from Prometheus
type MetricsCollector struct {
	client v1.API
}

// MetricPoint represents a single metric data point
type MetricPoint struct {
	Timestamp time.Time         `json:"timestamp"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels"`
}

// NewMetricsCollector creates a new Prometheus metrics collector
func NewMetricsCollector(endpoint string) (*MetricsCollector, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("prometheus endpoint not configured")
	}

	client, err := api.NewClient(api.Config{
		Address: endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus client: %w", err)
	}

	return &MetricsCollector{
		client: v1.NewAPI(client),
	}, nil
}

// FetchMetricHistory fetches metric history from Prometheus
// query: PromQL query, e.g., `rate(http_errors_total[5m])`
func (m *MetricsCollector) FetchMetricHistory(ctx context.Context, query string, since, until time.Time, step time.Duration) ([]MetricPoint, error) {
	r := v1.Range{
		Start: since,
		End:   until,
		Step:  step,
	}

	result, warnings, err := m.client.QueryRange(ctx, query, r)
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}

	if len(warnings) > 0 {
		fmt.Printf("Prometheus warnings: %v\n", warnings)
	}

	// Convert to MetricPoint
	var points []MetricPoint

	switch result.Type() {
	case model.ValMatrix:
		matrix := result.(model.Matrix)
		for _, stream := range matrix {
			labels := make(map[string]string)
			for k, v := range stream.Metric {
				labels[string(k)] = string(v)
			}

			for _, value := range stream.Values {
				point := MetricPoint{
					Timestamp: value.Timestamp.Time(),
					Value:     float64(value.Value),
					Labels:    labels,
				}
				points = append(points, point)
			}
		}
	default:
		return nil, fmt.Errorf("unexpected result type: %s", result.Type())
	}

	return points, nil
}

// FetchCurrentValue fetches the current value of a metric
func (m *MetricsCollector) FetchCurrentValue(ctx context.Context, query string) (float64, map[string]string, error) {
	result, warnings, err := m.client.Query(ctx, query, time.Now())
	if err != nil {
		return 0, nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}

	if len(warnings) > 0 {
		fmt.Printf("Prometheus warnings: %v\n", warnings)
	}

	switch result.Type() {
	case model.ValVector:
		vector := result.(model.Vector)
		if len(vector) == 0 {
			return 0, nil, fmt.Errorf("no data returned")
		}

		sample := vector[0]
		labels := make(map[string]string)
		for k, v := range sample.Metric {
			labels[string(k)] = string(v)
		}

		return float64(sample.Value), labels, nil
	default:
		return 0, nil, fmt.Errorf("unexpected result type: %s", result.Type())
	}
}

// FetchErrorRate fetches the error rate for a service
func (m *MetricsCollector) FetchErrorRate(ctx context.Context, service string, duration time.Duration) ([]MetricPoint, error) {
	query := fmt.Sprintf(`rate(http_errors_total{service="%s"}[%s])`, service, duration)
	until := time.Now()
	since := until.Add(-duration)
	step := duration / 60 // 60 data points

	return m.FetchMetricHistory(ctx, query, since, until, step)
}
