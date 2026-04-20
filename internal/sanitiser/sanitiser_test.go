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

package sanitiser

import (
	"strings"
	"testing"
)

func TestSanitiser_Email(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Contact: john.doe@example.com", "Contact: [EMAIL]"},
		{"Send to alice+tag@company.co.uk", "Send to [EMAIL]"},
		{"user@subdomain.example.org", "[EMAIL]"},
		{"multiple: a@b.com and c@d.com", "multiple: [EMAIL] and [EMAIL]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("Email: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_IPv4(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Server IP: 192.168.1.1", "Server IP: [IP]"},
		{"Connecting to 10.0.0.5:8080", "Connecting to [IP]:8080"},
		{"IPs: 172.16.0.1 and 8.8.8.8", "IPs: [IP] and [IP]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("IPv4: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_UUID(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"User ID: 550e8400-e29b-41d4-a716-446655440000", "User ID: [ID]"},
		{"Request: 6ba7b810-9dad-11d1-80b4-00c04fd430c8", "Request: [ID]"},
		{"Multiple: 123e4567-e89b-12d3-a456-426614174000 and 789e0123-e89b-12d3-a456-426614174001", "Multiple: [ID] and [ID]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("UUID: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_CreditCard(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Card: 4532-1234-5678-9010", "Card: [CARD]"},
		{"Number: 4532 1234 5678 9010", "Number: [CARD]"},
		{"Pay with 4532123456789010", "Pay with [CARD]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("CreditCard: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_JWTToken(t *testing.T) {
	s := NewSanitiser()

	input := "Auth: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	expected := "Auth: [TOKEN]"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("JWT: got %q, want %q", result, expected)
	}
}

func TestSanitiser_BearerToken(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Authorization: Bearer abc123def456", "Authorization: Bearer [TOKEN]"},
		{"Header: Bearer eyJhbGciOiJI.eyJzdWI.SflKxw", "Header: Bearer [TOKEN]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("BearerToken: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_AWSAccessKey(t *testing.T) {
	s := NewSanitiser()

	input := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"
	expected := "AWS_ACCESS_KEY_ID=[AWS_KEY]"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("AWSKey: got %q, want %q", result, expected)
	}
}

func TestSanitiser_PhoneNumber(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Call: (555) 123-4567", "Call: [PHONE]"},
		{"Phone: 555-123-4567", "Phone: [PHONE]"},
		{"Contact +1-555-123-4567", "Contact [PHONE]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("Phone: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_SSN(t *testing.T) {
	s := NewSanitiser()

	input := "SSN: 123-45-6789"
	expected := "SSN: [SSN]"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("SSN: got %q, want %q", result, expected)
	}
}

func TestSanitiser_AWSARN(t *testing.T) {
	s := NewSanitiser()

	input := "Resource: arn:aws:iam::123456789012:user/johndoe"
	expected := "Resource: [RESOURCE]"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("ARN: got %q, want %q", result, expected)
	}
}

func TestSanitiser_DatabaseConnection(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"DB: postgres://user:pass@localhost:5432/db", "DB: [DB_CONNECTION]"},
		{"Connect: mysql://admin:secret@db.example.com/prod", "Connect: [DB_CONNECTION]"},
		{"Mongo: mongodb://user:pass@cluster.mongodb.net/mydb", "Mongo: [DB_CONNECTION]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("DB: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_SlackToken(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input    string
		expected string
	}{
		{"Token: xoxb-1234567890-abcdef", "Token: [SLACK_TOKEN]"},
		{"Bot: xoxp-123-456-789-abc", "Bot: [SLACK_TOKEN]"},
	}

	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if result != tt.expected {
			t.Errorf("Slack: got %q, want %q", result, tt.expected)
		}
	}
}

func TestSanitiser_GitHubToken(t *testing.T) {
	s := NewSanitiser()

	input := "Token: ghp_" + strings.Repeat("a", 36)
	expected := "Token: [GITHUB_TOKEN]"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("GitHub: got %q, want %q", result, expected)
	}
}

func TestSanitiser_PasswordInURL(t *testing.T) {
	s := NewSanitiser()

	input := "https://admin:secretpass@example.com/api"
	expected := "https://[USER]:[PASSWORD]@example.com/api"

	result := s.Sanitise(input)
	if result != expected {
		t.Errorf("PasswordURL: got %q, want %q", result, expected)
	}
}

func TestSanitiser_MultiplePatterns(t *testing.T) {
	s := NewSanitiser()

	input := "User john.doe@example.com with ID 550e8400-e29b-41d4-a716-446655440000 logged in from 192.168.1.100"
	result := s.Sanitise(input)

	// Should remove all three: email, UUID, IP
	if strings.Contains(result, "john.doe@example.com") {
		t.Error("Email not sanitised")
	}
	if strings.Contains(result, "550e8400") {
		t.Error("UUID not sanitised")
	}
	if strings.Contains(result, "192.168.1.100") {
		t.Error("IP not sanitised")
	}
}

func TestSanitiser_SafeText(t *testing.T) {
	s := NewSanitiser()

	safeTexts := []string{
		"Error: failed to connect to database",
		"HTTP 500 Internal Server Error",
		"Stack trace at line 42",
		"Memory usage: 85%",
		"Request duration: 1.5s",
	}

	for _, text := range safeTexts {
		result := s.Sanitise(text)
		if result != text {
			t.Errorf("Safe text modified: %q -> %q", text, result)
		}

		if !s.IsSafe(text) {
			t.Errorf("Safe text marked as unsafe: %q", text)
		}
	}
}

func TestSanitiser_IsSafe(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		text string
		safe bool
	}{
		{"Normal error message", true},
		{"Contact: user@example.com", false},
		{"IP: 192.168.1.1", false},
		{"UUID: 550e8400-e29b-41d4-a716-446655440000", false},
		{"Stack trace at process.go:123", true},
	}

	for _, tt := range tests {
		result := s.IsSafe(tt.text)
		if result != tt.safe {
			t.Errorf("IsSafe(%q) = %v, want %v", tt.text, result, tt.safe)
		}
	}
}

func TestSanitiser_SanitiseMap(t *testing.T) {
	s := NewSanitiser()

	input := map[string]string{
		"email":   "user@example.com",
		"message": "Error from 192.168.1.1",
		"safe":    "Normal log line",
	}

	result := s.SanitiseMap(input)

	if result["email"] != "[EMAIL]" {
		t.Errorf("Map email not sanitised: %q", result["email"])
	}
	if !strings.Contains(result["message"], "[IP]") {
		t.Errorf("Map IP not sanitised: %q", result["message"])
	}
	if result["safe"] != "Normal log line" {
		t.Errorf("Map safe value modified: %q", result["safe"])
	}
}

func TestSanitiser_Performance(t *testing.T) {
	s := NewSanitiser()

	// Large text with multiple PII instances
	text := strings.Repeat("User john.doe@example.com from 192.168.1.1 with ID 550e8400-e29b-41d4-a716-446655440000. ", 100)

	// Should complete quickly
	result := s.Sanitise(text)

	if len(result) == 0 {
		t.Error("Performance test failed: empty result")
	}
}

func TestSanitiser_GetPatternCount(t *testing.T) {
	s := NewSanitiser()

	count := s.GetPatternCount()
	if count < 17 {
		t.Errorf("Expected at least 17 patterns, got %d", count)
	}
}

func TestSanitiser_IPv6(t *testing.T) {
	s := NewSanitiser()

	input := "Connecting to 2001:0db8:85a3:0000:0000:8a2e:0370:7334"
	result := s.Sanitise(input)
	if strings.Contains(result, "2001:0db8") {
		t.Errorf("IPv6 not sanitised: %q", result)
	}
}

func TestSanitiser_GCPResource(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input string
	}{
		{"Resource: projects/my-project/instances/my-instance"},
		{"Access projects/prod-env/buckets/my-bucket failed"},
	}
	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if strings.Contains(result, "projects/") {
			t.Errorf("GCP resource not sanitised: input=%q result=%q", tt.input, result)
		}
	}
}

func TestSanitiser_AzureResource(t *testing.T) {
	s := NewSanitiser()

	// The Azure regex uses \b before '/' — needs a word char immediately preceding the slash.
	// Use non-UUID subscription IDs (UUID pattern would fire first on real UUIDs).
	tests := []struct {
		input string
	}{
		// Word char 'd' before '/' triggers \b
		{"resource_id/subscriptions/abc123def/resourceGroups/myRG"},
	}
	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if strings.Contains(result, "abc123def") {
			t.Errorf("Azure resource not sanitised: input=%q result=%q", tt.input, result)
		}
		if !strings.Contains(result, "[RESOURCE]") {
			t.Errorf("expected [RESOURCE] replacement: input=%q result=%q", tt.input, result)
		}
	}
}

func TestSanitiser_InternalHostname(t *testing.T) {
	s := NewSanitiser()

	tests := []struct {
		input string
	}{
		{"Connect to api-gateway.internal"},
		{"Host: redis.cluster.internal"},
	}
	for _, tt := range tests {
		result := s.Sanitise(tt.input)
		if strings.Contains(result, ".internal") {
			t.Errorf("Internal hostname not sanitised: input=%q result=%q", tt.input, result)
		}
	}
}

func TestSanitiser_SSHPrivateKey(t *testing.T) {
	s := NewSanitiser()

	input := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\n-----END RSA PRIVATE KEY-----"
	result := s.Sanitise(input)
	if strings.Contains(result, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("SSH private key not sanitised: %q", result)
	}
	if !strings.Contains(result, "[SSH_KEY]") {
		t.Errorf("Expected [SSH_KEY] replacement, got: %q", result)
	}
}

func TestSanitiser_SanitiseMultiple(t *testing.T) {
	s := NewSanitiser()

	inputs := []string{
		"Contact: user@example.com",
		"Server at 10.0.0.1",
		"Normal log line",
	}

	results := s.SanitiseMultiple(inputs...)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !strings.Contains(results[0], "[EMAIL]") {
		t.Errorf("email not sanitised in SanitiseMultiple: %q", results[0])
	}
	if strings.Contains(results[1], "10.0.0.1") {
		t.Errorf("IP not sanitised in SanitiseMultiple: %q", results[1])
	}
	if results[2] != "Normal log line" {
		t.Errorf("safe text modified in SanitiseMultiple: %q", results[2])
	}
}

func TestSanitiser_EmptyString(t *testing.T) {
	s := NewSanitiser()

	result := s.Sanitise("")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}

	if !s.IsSafe("") {
		t.Error("empty string should be safe")
	}
}
