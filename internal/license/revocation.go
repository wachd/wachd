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

package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrRevoked is returned by CheckRevocation when the license JTI appears
// on the signed revocation list. Callers should drop to OSS limits.
var ErrRevoked = errors.New("license has been revoked — contact sales@wachd.io to renew")

// revocationEndpoint is the URL fetched on startup. Hardcoded — not
// customer-configurable. Points at the wachd.ee licensing server.
const revocationEndpoint = "https://license.wachd.io/api/v1/revoked"

// revocationTimeout is the maximum time allowed for the revocation check.
// Intentionally short — a slow endpoint must never delay pod startup.
const revocationTimeout = 5 * time.Second

// revocationBodyLimit caps the response body to prevent memory exhaustion.
const revocationBodyLimit = 64 * 1024 // 64 KB — ample for any revocation list

// revocationResponse is the JSON shape returned by the revocation endpoint.
type revocationResponse struct {
	Revoked  []string `json:"revoked"`
	IssuedAt int64    `json:"issued_at"`
	KID      string   `json:"kid"` // unsigned key-selection hint
	Sig      string   `json:"sig"` // base64url Ed25519 signature over revokedPayload bytes
}

// revokedPayload is exactly what the wachd.ee server signs.
// Field order is fixed by struct declaration — json.Marshal is deterministic
// for structs, so signer and verifier always produce identical bytes.
type revokedPayload struct {
	Revoked  []string `json:"revoked"`
	IssuedAt int64    `json:"issued_at"`
}

// CheckRevocation fetches the signed revocation list and returns ErrRevoked
// if jti appears on it.
//
// Failure modes — all return nil so a network issue never blocks a legitimate
// deployment:
//
//   - Endpoint unreachable (air-gapped, DNS failure, timeout) → nil
//   - Non-200 response → nil
//   - Malformed response body → nil
//
// The only non-nil errors are:
//
//   - ErrRevoked — jti is on the list; caller should drop to OSS limits
//   - Any other error — check was inconclusive (unknown kid, bad signature);
//     caller should log a warning and proceed with the existing license
func CheckRevocation(ctx context.Context, jti string) error {
	return checkRevocation(ctx, jti, revocationEndpoint, publicKeys)
}

// CheckRevocationWithConfig is like CheckRevocation but accepts an explicit
// endpoint URL and public key map. Intended for tests only.
func CheckRevocationWithConfig(ctx context.Context, jti, endpoint string, keys map[string]string) error {
	return checkRevocation(ctx, jti, endpoint, keys)
}

func checkRevocation(ctx context.Context, jti, endpoint string, keys map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, revocationTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		// Only fails on an invalid URL — can't happen with a hardcoded endpoint.
		return fmt.Errorf("build revocation request: %w", err)
	}
	req.Header.Set("User-Agent", "wachd/1 license-revocation-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Unreachable — air-gapped or transient network issue. Proceed.
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Unexpected status — don't block on endpoint issues.
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, revocationBodyLimit))
	if err != nil || len(body) == 0 {
		return nil
	}

	var rev revocationResponse
	if err := json.Unmarshal(body, &rev); err != nil {
		return nil
	}

	// Look up the public key by kid.
	pubKeyHex, ok := keys[rev.KID]
	if !ok {
		// Unknown kid — binary may need upgrading, but don't block the deployment.
		return fmt.Errorf("revocation list uses unknown kid %q — binary may need upgrading", rev.KID)
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return errors.New("embedded public key is invalid — please report this")
	}

	// Reconstruct exactly the bytes the signer produced:
	// json.Marshal(revokedPayload{Revoked, IssuedAt}) — same struct, same order.
	if rev.Revoked == nil {
		rev.Revoked = []string{} // match the signer's nil-guard
	}
	payload, err := json.Marshal(revokedPayload{Revoked: rev.Revoked, IssuedAt: rev.IssuedAt})
	if err != nil {
		return nil // json.Marshal on this struct cannot fail in practice
	}

	sig, err := base64.RawURLEncoding.DecodeString(rev.Sig)
	if err != nil {
		return fmt.Errorf("revocation list has malformed signature: %w", err)
	}

	if !ed25519.Verify(pubKeyBytes, payload, sig) {
		return errors.New("revocation list signature verification failed — endpoint may be compromised")
	}

	// Signature is valid. Check if this license's JTI is revoked.
	for _, revoked := range rev.Revoked {
		if revoked == jti {
			return ErrRevoked
		}
	}

	return nil
}
