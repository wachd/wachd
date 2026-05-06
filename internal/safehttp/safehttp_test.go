package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

type fakeResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f.addrs, f.err
}

func TestDialContextRejectsPrivateDNSResolution(t *testing.T) {
	dialCalled := false

	transport := NewTransport(
		WithResolver(fakeResolver{
			addrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
		}),
		WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("should not dial")
		}),
	)

	_, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Fatal("expected private DNS result to be rejected")
	}
	if !strings.Contains(err.Error(), "blocked private or local IP address") {
		t.Fatalf("expected private IP error, got: %v", err)
	}
	if dialCalled {
		t.Fatal("dial should not be called after private DNS result")
	}
}

func TestDialContextRejectsMixedPublicAndPrivateDNSResults(t *testing.T) {
	dialCalled := false

	transport := NewTransport(
		WithResolver(fakeResolver{
			addrs: []net.IPAddr{
				{IP: net.ParseIP("203.0.113.10")},
				{IP: net.ParseIP("169.254.169.254")},
			},
		}),
		WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("should not dial")
		}),
	)

	_, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Fatal("expected mixed public/private DNS results to be rejected")
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Fatalf("expected error to mention blocked metadata IP, got: %v", err)
	}
	if dialCalled {
		t.Fatal("dial should not be called when any resolved IP is blocked")
	}
}

func TestDialContextDialsValidatedResolvedIPNotOriginalHostname(t *testing.T) {
	expectedErr := errors.New("stop after recording dial address")
	var dialedAddress string

	transport := NewTransport(
		WithResolver(fakeResolver{
			addrs: []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}},
		}),
		WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialedAddress = address
			return nil, expectedErr
		}),
	)

	_, err := transport.DialContext(context.Background(), "tcp", "example.com:443")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected fake dial error, got: %v", err)
	}

	if dialedAddress != "203.0.113.10:443" {
		t.Fatalf("expected dial to use resolved IP, got %q", dialedAddress)
	}
}

func TestRoundTripRejectsPrivateURLBeforeNetworkDial(t *testing.T) {
	dialCalled := false

	roundTripper := NewRoundTripper(
		WithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("should not dial")
		}),
	)

	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/admin", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = roundTripper.RoundTrip(req)
	if err == nil {
		t.Fatal("expected private request URL to be rejected")
	}
	if dialCalled {
		t.Fatal("dial should not be called for blocked request URL")
	}
}

func TestValidateRedirectRejectsPrivateRedirectTarget(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	err = ValidateRedirect(req, nil)
	if err == nil {
		t.Fatal("expected private redirect target to be rejected")
	}
	if !strings.Contains(err.Error(), "blocked redirect target") {
		t.Fatalf("expected redirect error, got: %v", err)
	}
}

func TestDenyRedirectStopsRedirects(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com/redirected", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	err = DenyRedirect(req, nil)
	if !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("expected http.ErrUseLastResponse, got: %v", err)
	}
}
