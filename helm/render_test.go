//go:build integration

// Package helm_render_test verifies that the Helm chart templates render
// the expected environment variables when APNs and FCM are enabled.
//
// Run with:
//
//	go test -tags integration -v ./helm/
package helm_render_test

import (
	"os/exec"
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
