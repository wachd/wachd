package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateLokiLabelKey(t *testing.T) {
	ok := map[string]string{
		"":                       "service", // default, backwards compatible
		"   ":                    "service",
		"service":                "service",
		"app":                    "app",
		"app_kubernetes_io_name": "app_kubernetes_io_name",
		"_private":               "_private",
		" app ":                  "app",
	}
	for in, want := range ok {
		got, err := ValidateLokiLabelKey(in)
		if err != nil || got != want {
			t.Errorf("ValidateLokiLabelKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestValidateLokiLabelKey_Rejects(t *testing.T) {
	bad := []string{
		`app"} or {x="`,          // quote / brace injection
		"app}",                   // selector break-out
		"app=\"x\"",              // matcher smuggling
		"app,namespace",          // second matcher
		"app namespace",          // whitespace
		"9app",                   // cannot start with a digit
		"app\nname",              // newline
		"app|~\".*\"",            // operator smuggling
		"aß",                     // non-ASCII
		strings.Repeat("a", 254), // too long
		"{service",               // stray brace
		"app\\",                  // backslash
		"app.kubernetes.io/name", // dotted Kubernetes key: not a Loki label name
		"app-name",               // dash
		"kubernetes.io/name",
	}
	for _, in := range bad {
		if got, err := ValidateLokiLabelKey(in); err == nil {
			t.Errorf("ValidateLokiLabelKey(%q) accepted as %q", in, got)
		}
	}
}

// A dotted Kubernetes key must be rejected with a hint, never silently rewritten.
func TestValidateLokiLabelKey_DottedKeyHintsAtTheLokiName(t *testing.T) {
	_, err := ValidateLokiLabelKey("app.kubernetes.io/name")
	if err == nil || !strings.Contains(err.Error(), "app_kubernetes_io_name") {
		t.Fatalf("want an error pointing at app_kubernetes_io_name, got %v", err)
	}
}

func TestLokiStreamSelector(t *testing.T) {
	cases := []struct{ key, value, want string }{
		{"", "checkout-api", `{service="checkout-api"}`},
		{"service", "checkout-api", `{service="checkout-api"}`},
		{"app", "checkout-service", `{app="checkout-service"}`},
		{"app_kubernetes_io_name", "checkout", `{app_kubernetes_io_name="checkout"}`},
	}
	for _, c := range cases {
		got, err := LokiStreamSelector(c.key, c.value)
		if err != nil || got != c.want {
			t.Errorf("LokiStreamSelector(%q,%q) = %q, %v; want %q", c.key, c.value, got, err, c.want)
		}
	}
}

// A service name can come from an alert payload, so it must never be able to
// change the structure of the query.
func TestLokiStreamSelector_EscapesValue(t *testing.T) {
	for _, value := range []string{
		`x"} |~ ".*" or {service="y`,
		`back\slash`,
		"new\nline",
		`quote"inside`,
		"tab\there",
	} {
		sel, err := LokiStreamSelector("app", value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sel, `{app="`) || !strings.HasSuffix(sel, `"}`) {
			t.Errorf("selector structure broken for %q: %s", value, sel)
		}
		inner := sel[len(`{app="`) : len(sel)-len(`"}`)]
		// every double quote inside the value must be escaped
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' {
				i++ // skip the escaped character
				continue
			}
			if inner[i] == '"' {
				t.Errorf("unescaped quote in %q -> %s", value, sel)
			}
		}
		if strings.Contains(sel, "\n") {
			t.Errorf("raw newline in selector for %q", value)
		}
	}
}

func TestErrorLogsQuery_KeepsFilterAndRejectsBadKey(t *testing.T) {
	q, err := errorLogsQuery("app", "checkout")
	if err != nil || q != `{app="checkout"} |~ "(?i)(error|critical|fatal)"` {
		t.Fatalf("query = %q, %v", q, err)
	}
	if _, err := errorLogsQuery(`a"}`, "checkout"); err == nil {
		t.Fatal("an invalid label key must fail, not fall back to service")
	}
}

// What is actually sent to Loki.
func lokiQuerySeen(t *testing.T, configure func(*LogsCollector) *LogsCollector, service string) (string, error) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer srv.Close()
	c := configure(newLogsCollectorWithClient(srv.URL, srv.Client()))
	_, err := c.FetchErrorLogs(context.Background(), service, time.Now().Add(-time.Hour), time.Now(), 10)
	return got, err
}

func TestFetchErrorLogs_SendsConfiguredLabel(t *testing.T) {
	filter := ` |~ "(?i)(error|critical|fatal)"`
	for name, tc := range map[string]struct {
		cfg  func(*LogsCollector) *LogsCollector
		want string
	}{
		"unconfigured stays service":            {func(c *LogsCollector) *LogsCollector { return c }, `{service="checkout"}` + filter},
		"explicit empty stays service":          {func(c *LogsCollector) *LogsCollector { return c.WithLabelKey("") }, `{service="checkout"}` + filter},
		"app":                                   {func(c *LogsCollector) *LogsCollector { return c.WithLabelKey("app") }, `{app="checkout"}` + filter},
		"kubernetes pod label as Loki names it": {func(c *LogsCollector) *LogsCollector { return c.WithLabelKey("app_kubernetes_io_name") }, `{app_kubernetes_io_name="checkout"}` + filter},
	} {
		got, err := lokiQuerySeen(t, tc.cfg, "checkout")
		if err != nil || got != tc.want {
			t.Errorf("%s: query %q, err %v; want %q", name, got, err, tc.want)
		}
	}
}

func TestFetchErrorLogs_InvalidKeyIsAnErrorAndSendsNothing(t *testing.T) {
	got, err := lokiQuerySeen(t, func(c *LogsCollector) *LogsCollector { return c.WithLabelKey(`app"}`) }, "checkout")
	if err == nil || got != "" {
		t.Fatalf("invalid key: err=%v, query sent=%q", err, got)
	}
}

// The Grafana MCP path builds its own LogQL and must honour the same setting.
func TestGrafanaMCP_UsesConfiguredLokiLabel(t *testing.T) {
	var logql string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "s")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"serverInfo": map[string]string{"name": "grafana"}})})
		case "tools/call":
			p := req.Params.(map[string]interface{})
			switch p["name"] {
			case toolListDatasources:
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": []map[string]string{{"uid": "loki-uid", "type": "loki", "name": "Loki"}}})})
			case toolQueryLokiLogs:
				logql, _ = p["arguments"].(map[string]interface{})["logql"].(string)
				_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: mustRawJSON(t, map[string]any{"structuredContent": map[string]any{"logs": []any{}}})})
			}
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0"}`))
		}
	}))
	defer srv.Close()

	for key, want := range map[string]string{"": `{service="api"}`, "app": `{app="api"}`, "app_kubernetes_io_name": `{app_kubernetes_io_name="api"}`} {
		c := newGrafanaMCPCollectorWithClient(srv.URL, "t", srv.Client())
		c.SetLokiLabelKey(key)
		logql = ""
		if _, err := c.FetchErrorLogs(context.Background(), "api", time.Now().Add(-time.Hour), time.Now(), 10); err != nil {
			t.Fatalf("key %q: %v", key, err)
		}
		if !strings.HasPrefix(logql, want) {
			t.Errorf("key %q: logql %q, want prefix %q", key, logql, want)
		}
	}
	c := newGrafanaMCPCollectorWithClient(srv.URL, "t", srv.Client())
	c.SetLokiLabelKey(`x"}`)
	if _, err := c.FetchErrorLogs(context.Background(), "api", time.Now().Add(-time.Hour), time.Now(), 10); err == nil {
		t.Error("invalid key must be an error on the MCP path too")
	}
}

// Empty and failing Loki responses behave the same whichever label is used.
func TestFetchErrorLogs_EmptyResponseIsNotAnError(t *testing.T) {
	for _, key := range []string{"", "app"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
		}))
		c := newLogsCollectorWithClient(srv.URL, srv.Client()).WithLabelKey(key)
		logs, err := c.FetchErrorLogs(context.Background(), "checkout", time.Now().Add(-time.Hour), time.Now(), 10)
		srv.Close()
		if err != nil || len(logs) != 0 {
			t.Errorf("key %q: logs=%d err=%v; want no logs and no error", key, len(logs), err)
		}
	}
}

func TestFetchErrorLogs_LokiErrorStatusIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "parse error", http.StatusBadRequest)
	}))
	defer srv.Close()
	c := newLogsCollectorWithClient(srv.URL, srv.Client()).WithLabelKey("app")
	if _, err := c.FetchErrorLogs(context.Background(), "checkout", time.Now().Add(-time.Hour), time.Now(), 10); err == nil {
		t.Fatal("a Loki error must be surfaced, not hidden as 'no logs'")
	}
}
