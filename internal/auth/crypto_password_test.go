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

package auth

import (
	"strings"
	"testing"

	"github.com/wachd/wachd/internal/store"
)

// ── Encryptor ─────────────────────────────────────────────────────────────────

const testHexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNewEncryptor_Valid(t *testing.T) {
	enc, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil encryptor")
	}
}

func TestNewEncryptor_InvalidHex(t *testing.T) {
	_, err := NewEncryptor("not-valid-hex!!!")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestNewEncryptor_WrongLength(t *testing.T) {
	// 16 bytes (32 hex chars) instead of 32 bytes (64 hex chars)
	_, err := NewEncryptor("0123456789abcdef0123456789abcdef")
	if err == nil {
		t.Error("expected error for wrong key length")
	}
}

func TestEncryptor_EncryptDecrypt_Roundtrip(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)

	plaintexts := []string{
		"hello world",
		"sk-ant-api03-supersecretkey",
		"postgres://user:pass@localhost:5432/db",
		"",
		strings.Repeat("x", 1000),
	}

	for _, plain := range plaintexts {
		ciphertext, err := enc.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", plain, err)
		}
		if ciphertext == plain && plain != "" {
			t.Errorf("Encrypt should not return plaintext as ciphertext")
		}

		decrypted, err := enc.Decrypt(ciphertext)
		if err != nil {
			t.Fatalf("Decrypt error: %v", err)
		}
		if decrypted != plain {
			t.Errorf("roundtrip failed: got %q, want %q", decrypted, plain)
		}
	}
}

func TestEncryptor_Encrypt_NonDeterministic(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)

	ct1, _ := enc.Encrypt("same plaintext")
	ct2, _ := enc.Encrypt("same plaintext")
	if ct1 == ct2 {
		t.Error("expected different ciphertexts for same plaintext (random nonce)")
	}
}

func TestEncryptor_Decrypt_InvalidBase64(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	_, err := enc.Decrypt("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestEncryptor_Decrypt_TooShort(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)
	// Valid base64 but too short (less than nonce size)
	import64 := "dGVzdA==" // "test" in base64, only 4 bytes
	_, err := enc.Decrypt(import64)
	if err == nil {
		t.Error("expected error for ciphertext shorter than nonce")
	}
}

func TestEncryptor_Decrypt_Tampered(t *testing.T) {
	enc, _ := NewEncryptor(testHexKey)

	ct, _ := enc.Encrypt("secret data")
	// Flip last byte to simulate tampering
	raw := []byte(ct)
	raw[len(raw)-1] ^= 0xFF
	_, err := enc.Decrypt(string(raw))
	if err == nil {
		t.Error("expected error for tampered ciphertext")
	}
}

func TestEncryptor_Decrypt_WrongKey(t *testing.T) {
	enc1, _ := NewEncryptor(testHexKey)
	enc2, _ := NewEncryptor("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")

	ct, _ := enc1.Encrypt("my secret")
	_, err := enc2.Decrypt(ct)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

// ── Password hashing ──────────────────────────────────────────────────────────

func TestHashPassword_And_CheckPassword(t *testing.T) {
	hash, err := HashPassword("MySecureP@ss1")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == "MySecureP@ss1" {
		t.Fatal("hash should not equal plaintext")
	}

	if err := CheckPassword(hash, "MySecureP@ss1"); err != nil {
		t.Errorf("CheckPassword with correct password failed: %v", err)
	}
	if err := CheckPassword(hash, "WrongPassword"); err == nil {
		t.Error("CheckPassword with wrong password should fail")
	}
}

func TestHashPassword_DifferentHashEachTime(t *testing.T) {
	h1, _ := HashPassword("password123")
	h2, _ := HashPassword("password123")
	if h1 == h2 {
		t.Error("expected different hashes for same password (bcrypt uses random salt)")
	}
}

// ── ValidatePolicy ────────────────────────────────────────────────────────────

func TestValidatePolicy_MinLength(t *testing.T) {
	policy := &store.PasswordPolicy{MinLength: 8}

	if err := ValidatePolicy("short", policy); err == nil {
		t.Error("expected error for password shorter than min length")
	}
	if err := ValidatePolicy("longenough", policy); err != nil {
		t.Errorf("unexpected error for valid length: %v", err)
	}
}

func TestValidatePolicy_RequireUppercase(t *testing.T) {
	policy := &store.PasswordPolicy{MinLength: 1, RequireUppercase: true}

	if err := ValidatePolicy("nouppercase", policy); err == nil {
		t.Error("expected error for no uppercase")
	}
	if err := ValidatePolicy("HasUpper", policy); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePolicy_RequireLowercase(t *testing.T) {
	policy := &store.PasswordPolicy{MinLength: 1, RequireLowercase: true}

	if err := ValidatePolicy("NOLOWERCASE", policy); err == nil {
		t.Error("expected error for no lowercase")
	}
	if err := ValidatePolicy("HasLower", policy); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePolicy_RequireNumber(t *testing.T) {
	policy := &store.PasswordPolicy{MinLength: 1, RequireNumber: true}

	if err := ValidatePolicy("NoNumbers!", policy); err == nil {
		t.Error("expected error for no digits")
	}
	if err := ValidatePolicy("Has1Digit", policy); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePolicy_RequireSpecial(t *testing.T) {
	policy := &store.PasswordPolicy{MinLength: 1, RequireSpecial: true}

	if err := ValidatePolicy("NoSpecial1", policy); err == nil {
		t.Error("expected error for no special char")
	}
	if err := ValidatePolicy("Has@Special", policy); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePolicy_AllRules(t *testing.T) {
	policy := &store.PasswordPolicy{
		MinLength:        12,
		RequireUppercase: true,
		RequireLowercase: true,
		RequireNumber:    true,
		RequireSpecial:   true,
	}

	valid := "MyP@ssw0rd!1"
	if err := ValidatePolicy(valid, policy); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}

	invalid := []struct {
		password string
		reason   string
	}{
		{"short1A!", "too short"},
		{"mylongpassword1!", "no uppercase"},
		{"MYLONGPASSWORD1!", "no lowercase"},
		{"MyLongPassword!", "no digit"},
		{"MyLongPassword1", "no special"},
	}
	for _, tc := range invalid {
		if err := ValidatePolicy(tc.password, policy); err == nil {
			t.Errorf("expected error for %s (%q)", tc.reason, tc.password)
		}
	}
}

func TestValidatePolicy_NoCharRules(t *testing.T) {
	// When no character-class rules are set, only min length is checked
	policy := &store.PasswordPolicy{
		MinLength:        4,
		RequireUppercase: false,
		RequireLowercase: false,
		RequireNumber:    false,
		RequireSpecial:   false,
	}

	if err := ValidatePolicy("abcd", policy); err != nil {
		t.Errorf("unexpected error for valid password with no char rules: %v", err)
	}
}
