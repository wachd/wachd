package main

import (
	"testing"

	"github.com/wachd/wachd/internal/collector"
	"github.com/wachd/wachd/internal/store"
)

func TestLokiLabelKey_DefaultsToEmpty(t *testing.T) {
	if got := lokiLabelKey(nil); got != "" {
		t.Errorf("nil config: %q", got)
	}
	if got := lokiLabelKey(&store.TeamConfig{}); got != "" {
		t.Errorf("unset key: %q", got)
	}
	k := "app_kubernetes_io_name"
	if got := lokiLabelKey(&store.TeamConfig{LokiLabelKey: &k}); got != k {
		t.Errorf("configured key: %q", got)
	}
}

// Collectors that cannot take a label key (test doubles, other backends) are left alone.
func TestApplyLokiLabelKey_OnlyTouchesCollectorsThatSupportIt(t *testing.T) {
	k := "app"
	cfg := &store.TeamConfig{LokiLabelKey: &k}
	applyLokiLabelKey(&mockMCPCollector{}, cfg) // must not panic
	real := collector.NewGrafanaMCPCollector("https://grafana.example.com/mcp", "t")
	applyLokiLabelKey(real, cfg)
}
