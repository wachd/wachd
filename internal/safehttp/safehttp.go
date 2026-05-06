package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/wachd/wachd/internal/validate"
)

// Resolver is the DNS lookup surface used by the safe dialer.
// It is intentionally tiny so tests can inject deterministic DNS answers.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type config struct {
	resolver    Resolver
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)
}

// Option configures the safe HTTP transport.
type Option func(*config)

// WithResolver overrides DNS resolution. It is primarily useful for tests.
func WithResolver(resolver Resolver) Option {
	return func(cfg *config) {
		if resolver != nil {
			cfg.resolver = resolver
		}
	}
}

// WithDialContext overrides the final TCP dial function.
// It is primarily useful for tests.
func WithDialContext(dialContext func(ctx context.Context, network, address string) (net.Conn, error)) Option {
	return func(cfg *config) {
		if dialContext != nil {
			cfg.dialContext = dialContext
		}
	}
}

// NewTransport returns an http.Transport that prevents DNS-rebinding SSRF.
//
// The dialer resolves the request hostname, validates every returned IP, and
// then dials one of the already-validated IPs directly. This avoids the common
// TOCTOU bug where code validates one DNS lookup but then hands the original
// hostname back to net.Dial, causing a second lookup at connection time.
func NewTransport(opts ...Option) *http.Transport {
	netDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	cfg := &config{
		resolver:    net.DefaultResolver,
		dialContext: netDialer.DialContext,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&safeDialer{
		resolver:    cfg.resolver,
		dialContext: cfg.dialContext,
	}).DialContext

	return base
}

// NewRoundTripper returns a RoundTripper that validates the request URL and
// uses NewTransport for DNS-rebinding-safe dials.
func NewRoundTripper(opts ...Option) http.RoundTripper {
	return &validatingRoundTripper{
		base: NewTransport(opts...),
	}
}

// CollectorClient returns a hardened client for collector GET/query traffic.
//
// Redirects are allowed only when each redirect target also passes URL
// validation. The redirected request still uses the safe transport.
func CollectorClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     NewRoundTripper(),
		CheckRedirect: ValidateRedirect,
	}
}

// WebhookClient returns a hardened client for webhook POST traffic.
//
// Webhook-style POSTs should not follow redirects at all. Returning
// http.ErrUseLastResponse makes net/http return the 3xx response to the caller
// without following it.
func WebhookClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		Transport:     NewRoundTripper(),
		CheckRedirect: DenyRedirect,
	}
}

// ValidateRedirect rejects redirects to URLs blocked by validate.EndpointURL.
func ValidateRedirect(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return errors.New("redirect target is missing URL")
	}

	if err := validate.EndpointURL(req.URL.String()); err != nil {
		return fmt.Errorf("blocked redirect target: %w", err)
	}

	return nil
}

// DenyRedirect disables redirects for webhook-style clients.
func DenyRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

type validatingRoundTripper struct {
	base http.RoundTripper
}

func (v *validatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("request is missing URL")
	}

	if err := validate.EndpointURL(req.URL.String()); err != nil {
		return nil, err
	}

	return v.base.RoundTrip(req)
}

type safeDialer struct {
	resolver    Resolver
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)
}

func (d *safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %q: %w", address, err)
	}

	ips, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses resolved for %q", host)
	}

	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("blocked private or local IP address %s for host %q", ip.String(), host)
		}
	}

	var lastErr error
	for _, ip := range ips {
		if !compatibleWithNetwork(network, ip) {
			continue
		}

		conn, err := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, fmt.Errorf("no resolved IPs for %q were compatible with network %q", host, network)
}

func (d *safeDialer) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{normalizeIP(ip)}, nil
	}

	addrs, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}

	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP == nil {
			continue
		}
		ips = append(ips, normalizeIP(addr.IP))
	}

	return ips, nil
}

func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}

	return ip
}

func compatibleWithNetwork(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}

func isBlockedIP(ip net.IP) bool {
	ip = normalizeIP(ip)

	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}
