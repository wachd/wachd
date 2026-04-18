package validate

import (
	"testing"
)

func TestEndpointURL_Allowed(t *testing.T) {
	cases := []string{
		"https://prometheus.example.com",
		"http://grafana.example.com:3000",
		"https://loki.acme.corp/loki/api/v1",
		"https://splunk.company.com:8089",
		"https://abc12345.live.dynatrace.com",
		"https://slack.example.com/services/webhook",
	}
	for _, u := range cases {
		if err := EndpointURL(u); err != nil {
			t.Errorf("allowed URL %q rejected: %v", u, err)
		}
	}
}

func TestEndpointURL_Blocked(t *testing.T) {
	cases := []struct {
		url    string
		reason string
	}{
		{"http://localhost/metrics", "localhost"},
		{"http://localhost.localdomain/metrics", "localhost.localdomain"},
		{"http://LOCALHOST/metrics", "localhost case-insensitive"},
		{"http://127.0.0.1:9090", "loopback IPv4"},
		{"http://[::1]:9090", "loopback IPv6"},
		{"http://10.0.0.1/loki", "RFC1918 10.x"},
		{"http://172.16.0.1/loki", "RFC1918 172.16.x"},
		{"http://192.168.1.1/loki", "RFC1918 192.168.x"},
		{"http://169.254.169.254/latest/meta-data/", "AWS/GCP metadata IP"},
		{"http://0.0.0.0/metrics", "0.0.0.0"},
		{"http://[::ffff:127.0.0.1]/metrics", "IPv4-mapped loopback"},
		{"http://[fc00::1]/metrics", "ULA IPv6"},
		{"http://internal.service.internal/api", ".internal suffix"},
		{"http://host.local/api", ".local suffix"},
		{"http://host.localdomain/api", ".localdomain suffix"},
		{"http://metadata/metrics", "metadata short hostname"},
		{"ftp://example.com/data", "non-http scheme"},
		{"file:///etc/passwd", "file scheme"},
	}
	for _, tc := range cases {
		if err := EndpointURL(tc.url); err == nil {
			t.Errorf("blocked URL %q (%s) was allowed", tc.url, tc.reason)
		}
	}
}
