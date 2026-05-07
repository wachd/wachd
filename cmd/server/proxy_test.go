package main

import (
	"net/http"
	"net/netip"
	"testing"
)

func mustParseCIDRs(t *testing.T, raw string) []netip.Prefix {
	t.Helper()

	prefixes, err := parseTrustedProxyCIDRs(raw)
	if err != nil {
		t.Fatalf("parseTrustedProxyCIDRs(%q): %v", raw, err)
	}

	return prefixes
}

func TestParseTrustedProxyCIDRsEmptyTrustsNothing(t *testing.T) {
	prefixes, err := parseTrustedProxyCIDRs("")
	if err != nil {
		t.Fatalf("expected empty config to parse, got: %v", err)
	}

	if len(prefixes) != 0 {
		t.Fatalf("expected no trusted proxies by default, got %v", prefixes)
	}
}

func TestParseTrustedProxyCIDRsRejectsInvalidCIDR(t *testing.T) {
	_, err := parseTrustedProxyCIDRs("10.0.0.0/8,not-a-cidr")
	if err == nil {
		t.Fatal("expected invalid CIDR to return an error")
	}
}

func TestWebhookClientIPIgnoresXForwardedForWithoutTrustedProxy(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "10.0.0.10:12345",
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.10"},
		},
	}

	got := webhookClientIP(r, nil)

	if got != "10.0.0.10" {
		t.Fatalf("expected RemoteAddr when no proxies are trusted, got %q", got)
	}
}

func TestWebhookClientIPIgnoresXForwardedForFromUntrustedRemote(t *testing.T) {
	trusted := mustParseCIDRs(t, "10.0.0.0/8")
	r := &http.Request{
		RemoteAddr: "203.0.113.5:12345",
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.10"},
		},
	}

	got := webhookClientIP(r, trusted)

	if got != "203.0.113.5" {
		t.Fatalf("expected untrusted RemoteAddr to win, got %q", got)
	}
}

func TestWebhookClientIPUsesRightToLeftXForwardedForFromTrustedProxy(t *testing.T) {
	trusted := mustParseCIDRs(t, "10.0.0.0/8,172.16.0.0/12")
	r := &http.Request{
		RemoteAddr: "10.0.0.10:12345",
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.10, 172.16.0.20"},
		},
	}

	got := webhookClientIP(r, trusted)

	if got != "198.51.100.10" {
		t.Fatalf("expected right-to-left parsing to skip trusted proxy hop, got %q", got)
	}
}

func TestWebhookClientIPRejectsMalformedXForwardedFor(t *testing.T) {
	trusted := mustParseCIDRs(t, "10.0.0.0/8")
	r := &http.Request{
		RemoteAddr: "10.0.0.10:12345",
		Header: http.Header{
			"X-Forwarded-For": []string{"198.51.100.10, not-an-ip"},
		},
	}

	got := webhookClientIP(r, trusted)

	if got != "10.0.0.10" {
		t.Fatalf("expected malformed XFF to fall back to RemoteAddr, got %q", got)
	}
}

func TestWebhookClientIPHandlesIPv6TrustedProxy(t *testing.T) {
	trusted := mustParseCIDRs(t, "2001:db8:1::/48")
	r := &http.Request{
		RemoteAddr: "[2001:db8:1::10]:12345",
		Header: http.Header{
			"X-Forwarded-For": []string{"2001:db8:2::20"},
		},
	}

	got := webhookClientIP(r, trusted)

	if got != "2001:db8:2::20" {
		t.Fatalf("expected IPv6 XFF client, got %q", got)
	}
}
