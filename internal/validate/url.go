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

package validate

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// EndpointURL rejects URLs that could be used for SSRF attacks.
// Only public http/https URLs are allowed — no private IP ranges, no localhost.
func EndpointURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https schemes are allowed")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		// DNS name — block known internal hostname patterns.
		lower := strings.ToLower(host)
		blocked := []string{
			"localhost",
			"localhost.localdomain",
			"metadata",        // GCP/AWS metadata short name in some configs
			"metadata.google",
		}
		for _, b := range blocked {
			if lower == b {
				return fmt.Errorf("private hostnames are not allowed")
			}
		}
		if strings.HasSuffix(lower, ".local") ||
			strings.HasSuffix(lower, ".internal") ||
			strings.HasSuffix(lower, ".localdomain") {
			return fmt.Errorf("private hostnames are not allowed")
		}
		return nil
	}
	// Block 0.0.0.0 explicitly — maps to all interfaces (loopback on Linux).
	if ip.Equal(net.IPv4zero) || ip.Equal(net.IPv6unspecified) {
		return fmt.Errorf("private IP addresses are not allowed")
	}
	// Unwrap IPv4-mapped IPv6 addresses (e.g. ::ffff:127.0.0.1).
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// Block RFC 1918, loopback, link-local, and metadata service ranges.
	privateRanges := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return fmt.Errorf("private IP addresses are not allowed")
		}
	}
	return nil
}
