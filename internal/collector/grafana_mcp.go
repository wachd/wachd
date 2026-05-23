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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wachd/wachd/internal/safehttp"
)

const (
	defaultGrafanaMCPTimeout         = 30 * time.Second
	defaultGrafanaMCPStep            = 30
	defaultGrafanaMCPLogLimit        = 50
	defaultGrafanaMCPSessionProtocol = "2025-03-26"
	toolListDatasources              = "list_datasources"
	toolQueryLokiLogs                = "query_loki_logs"
	toolQueryPrometheus              = "query_prometheus"
	datasourceTypeLoki               = "loki"
	datasourceTypePrometheus         = "prometheus"
)

type GrafanaMCPCollector struct {
	endpoint  string
	token     string
	client    *http.Client
	sessionID string
	nextID    int64
}

type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpInitializeResult struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpToolCallResult struct {
	StructuredContent interface{}      `json:"structuredContent,omitempty"`
	Content           []mcpContentItem `json:"content,omitempty"`
}

type mcpContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type grafanaDatasource struct {
	UID  string `json:"uid"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func NewGrafanaMCPCollector(endpoint, token string) *GrafanaMCPCollector {
	return newGrafanaMCPCollectorWithClient(endpoint, token, safehttp.CollectorClient(defaultGrafanaMCPTimeout))
}

func newGrafanaMCPCollectorWithClient(endpoint, token string, client *http.Client) *GrafanaMCPCollector {
	if client == nil {
		client = safehttp.CollectorClient(defaultGrafanaMCPTimeout)
	}
	return &GrafanaMCPCollector{
		endpoint: strings.TrimRight(endpoint, "/"),
		token:    token,
		client:   client,
		nextID:   1,
	}
}

func (g *GrafanaMCPCollector) FetchErrorLogs(ctx context.Context, service string, since, until time.Time, limit int) ([]LogLine, error) {
	if err := g.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultGrafanaMCPLogLimit
	}
	uid, err := g.lookupDatasourceUID(ctx, datasourceTypeLoki)
	if err != nil {
		return nil, err
	}
	result, err := g.callTool(ctx, toolQueryLokiLogs, map[string]interface{}{
		"datasourceUid": uid,
		"logql":         fmt.Sprintf(`{service=%q} |~ "(?i)(error|critical|fatal)"`, service),
		"startRfc3339":  since.UTC().Format(time.RFC3339),
		"endRfc3339":    until.UTC().Format(time.RFC3339),
		"limit":         limit,
		"direction":     "backward",
		"queryType":     "range",
	})
	if err != nil {
		return nil, err
	}
	logs := extractLogLines(result)
	if logs == nil {
		return []LogLine{}, nil
	}
	return logs, nil
}

func (g *GrafanaMCPCollector) FetchErrorRate(ctx context.Context, service string, duration time.Duration) ([]MetricPoint, error) {
	if err := g.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	uid, err := g.lookupDatasourceUID(ctx, datasourceTypePrometheus)
	if err != nil {
		return nil, err
	}
	until := time.Now().UTC()
	since := until.Add(-duration)
	result, err := g.callTool(ctx, toolQueryPrometheus, map[string]interface{}{
		"datasourceUid": uid,
		"expr":          fmt.Sprintf(`rate(http_errors_total{service=%q}[%s])`, service, duration),
		"queryType":     "range",
		"startTime":     since.Format(time.RFC3339),
		"endTime":       until.Format(time.RFC3339),
		"stepSeconds":   defaultGrafanaMCPStep,
	})
	if err != nil {
		return nil, err
	}
	points := extractMetricPoints(result)
	if points == nil {
		return []MetricPoint{}, nil
	}
	return points, nil
}

func (g *GrafanaMCPCollector) ensureInitialized(ctx context.Context) error {
	if g.endpoint == "" {
		return fmt.Errorf("grafana mcp endpoint not configured")
	}
	if g.sessionID != "" {
		return nil
	}

	var initResult mcpInitializeResult
	if err := g.rpc(ctx, mcpRequest{
		JSONRPC: "2.0",
		ID:      g.nextRequestID(),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": defaultGrafanaMCPSessionProtocol,
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "wachd",
				"version": "0.1.0",
			},
		},
	}, &initResult); err != nil {
		return fmt.Errorf("initialize grafana mcp session: %w", err)
	}
	return g.notifyInitialized(ctx)
}

func (g *GrafanaMCPCollector) notifyInitialized(ctx context.Context) error {
	return g.rpc(ctx, mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}, nil)
}

func (g *GrafanaMCPCollector) lookupDatasourceUID(ctx context.Context, wantedType string) (string, error) {
	result, err := g.callTool(ctx, toolListDatasources, map[string]interface{}{})
	if err != nil {
		return "", err
	}
	datasources := extractDatasources(result)
	for _, ds := range datasources {
		if strings.EqualFold(ds.Type, wantedType) && ds.UID != "" {
			return ds.UID, nil
		}
	}
	return "", fmt.Errorf("grafana mcp datasource %q not found", wantedType)
}

func (g *GrafanaMCPCollector) callTool(ctx context.Context, name string, arguments map[string]interface{}) (interface{}, error) {
	var result mcpToolCallResult
	if err := g.rpc(ctx, mcpRequest{
		JSONRPC: "2.0",
		ID:      g.nextRequestID(),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      name,
			"arguments": arguments,
		},
	}, &result); err != nil {
		return nil, fmt.Errorf("grafana mcp tool %s: %w", name, err)
	}
	if result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	for _, item := range result.Content {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(item.Text), &parsed); err == nil {
			return parsed, nil
		}
		return item.Text, nil
	}
	return nil, nil
}

func (g *GrafanaMCPCollector) rpc(ctx context.Context, req mcpRequest, out interface{}) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal mcp request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create mcp request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if g.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.token)
	}
	if g.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", g.sessionID)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("execute mcp request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("Mcp-Session-Id") != "" {
		g.sessionID = resp.Header.Get("Mcp-Session-Id")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mcp server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var payload []byte
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		payload, err = readSSEPayload(resp.Body)
	} else {
		payload, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}

	var rpcResp mcpResponse
	if err := json.Unmarshal(payload, &rpcResp); err != nil {
		return fmt.Errorf("decode mcp response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("mcp error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out == nil || len(rpcResp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode mcp result: %w", err)
	}
	return nil
}

func (g *GrafanaMCPCollector) nextRequestID() int64 {
	id := g.nextID
	g.nextID++
	return id
}

func readSSEPayload(body io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 4096)
	scanner.Buffer(buf, 1024*1024)
	var chunks []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			chunks = append(chunks, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read sse payload: %w", err)
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	return []byte(chunks[len(chunks)-1]), nil
}

func extractDatasources(result interface{}) []grafanaDatasource {
	var datasources []grafanaDatasource
	walkValues(result, func(value map[string]interface{}) {
		uid, _ := value["uid"].(string)
		typeName, _ := value["type"].(string)
		name, _ := value["name"].(string)
		if uid != "" && typeName != "" {
			datasources = append(datasources, grafanaDatasource{UID: uid, Type: typeName, Name: name})
		}
	})
	return datasources
}

func extractLogLines(result interface{}) []LogLine {
	var logs []LogLine
	walkValues(result, func(value map[string]interface{}) {
		ts, ok := parseFlexibleTime(value["timestamp"])
		if !ok {
			return
		}
		msg, _ := value["message"].(string)
		if strings.TrimSpace(msg) == "" {
			msg, _ = value["line"].(string)
		}
		if strings.TrimSpace(msg) == "" {
			return
		}
		labels := stringifyMap(value["labels"])
		level, _ := value["level"].(string)
		logs = append(logs, LogLine{Timestamp: ts, Message: msg, Level: level, Labels: labels})
	})
	return logs
}

func extractMetricPoints(result interface{}) []MetricPoint {
	var points []MetricPoint
	walkValues(result, func(value map[string]interface{}) {
		ts, ok := parseFlexibleTime(value["timestamp"])
		if !ok {
			return
		}
		val, ok := parseFlexibleFloat(value["value"])
		if !ok {
			return
		}
		labels := stringifyMap(value["labels"])
		if len(labels) == 0 {
			labels = stringifyMap(value["metric"])
		}
		points = append(points, MetricPoint{Timestamp: ts, Value: val, Labels: labels})
	})
	return points
}

func walkValues(value interface{}, onMap func(map[string]interface{})) {
	switch typed := value.(type) {
	case map[string]interface{}:
		onMap(typed)
		for _, child := range typed {
			walkValues(child, onMap)
		}
	case []interface{}:
		for _, child := range typed {
			walkValues(child, onMap)
		}
	}
}

func stringifyMap(value interface{}) map[string]string {
	typed, ok := value.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(typed))
	for k, v := range typed {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func parseFlexibleTime(value interface{}) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return time.Time{}, false
		}
		if ts, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return ts.UTC(), true
		}
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	}
	return time.Time{}, false
}

func parseFlexibleFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		var parsed float64
		if _, err := fmt.Sscanf(typed, "%f", &parsed); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
