//go:build integration

// Package helm_render_test verifies that the Helm chart templates render
// the expected environment variables when APNs and FCM are enabled.
//
// Run with:
//
//	go test -tags integration -v ./helm/
package helm_render_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const chartPath = "./wachd"

// helmTemplate runs `helm template` with the given extra --set flags and
// returns the rendered YAML as a string.
func helmTemplate(t *testing.T, sets ...string) string {
	t.Helper()
	args := []string{"template", "wachd", chartPath}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestHelmRender_APNs_EnvVarsPresent(t *testing.T) {
	out := helmTemplate(t,
		"config.notifications.apns.enabled=true",
		"config.notifications.apns.bundleId=io.wachd.app",
		"config.notifications.apns.production=true",
	)

	expected := []string{
		"APNS_KEY_ID",
		"APNS_TEAM_ID",
		"APNS_PRIVATE_KEY",
		"APNS_BUNDLE_ID",
		"APNS_PRODUCTION",
	}
	for _, env := range expected {
		if !strings.Contains(out, env) {
			t.Errorf("APNs enabled: expected env var %q in worker deployment, not found", env)
		}
	}
}

func TestHelmRender_APNs_Disabled_NoEnvVars(t *testing.T) {
	out := helmTemplate(t) // apns.enabled defaults to false

	apnsVars := []string{"APNS_KEY_ID", "APNS_TEAM_ID", "APNS_PRIVATE_KEY", "APNS_BUNDLE_ID", "APNS_PRODUCTION"}
	for _, env := range apnsVars {
		if strings.Contains(out, env) {
			t.Errorf("APNs disabled: env var %q should not appear in rendered output, but it does", env)
		}
	}
}

func TestHelmRender_FCM_EnvVarPresent(t *testing.T) {
	out := helmTemplate(t, "config.notifications.fcm.enabled=true")

	if !strings.Contains(out, "FCM_SERVICE_ACCOUNT_JSON") {
		t.Error("FCM enabled: expected FCM_SERVICE_ACCOUNT_JSON in worker deployment, not found")
	}
}

func TestHelmRender_FCM_Disabled_NoEnvVar(t *testing.T) {
	out := helmTemplate(t) // fcm.enabled defaults to false

	if strings.Contains(out, "FCM_SERVICE_ACCOUNT_JSON") {
		t.Error("FCM disabled: FCM_SERVICE_ACCOUNT_JSON should not appear in rendered output, but it does")
	}
}

// chartAppVersion reads appVersion from Chart.yaml.
func chartAppVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(chartPath + "/Chart.yaml")
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	m := regexp.MustCompile(`(?m)^appVersion:\s*"?([^"\s]+)"?\s*$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("appVersion not found in Chart.yaml")
	}
	return string(m[1])
}

// A default install must deploy the image that matches the chart's appVersion.
func TestHelmRender_DefaultImageTagIsAppVersion(t *testing.T) {
	out := helmTemplate(t)
	want := "image: ghcr.io/wachd/wachd:" + chartAppVersion(t)

	if got := strings.Count(out, want); got != 3 {
		t.Errorf("expected %q in server, worker and web deployments (3), found %d", want, got)
	}
}

func TestHelmRender_ExplicitImageTagWins(t *testing.T) {
	out := helmTemplate(t, "image.tag=9.9.9")

	if !strings.Contains(out, "image: ghcr.io/wachd/wachd:9.9.9") {
		t.Error("image.tag override not applied to rendered deployments")
	}
}
