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
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/wachd/wachd/internal/store"
	"golang.org/x/oauth2"
)

// OIDCProvider holds the configured OAuth2 and OIDC objects for Microsoft Entra.
type OIDCProvider struct {
	oauth2Config *oauth2.Config
	verifier     *gooidc.IDTokenVerifier
	TenantID     string
	ClientID     string
}

// NewOIDCProvider initialises the Microsoft Entra OIDC provider using auto-discovery.
func NewOIDCProvider(ctx context.Context, tenantID, clientID, clientSecret, redirectURI string) (*OIDCProvider, error) {
	issuer := fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID)

	provider, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider discovery: %w", err)
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{gooidc.ScopeOpenID, "profile", "email", "offline_access"},
	}

	verifier := provider.Verifier(&gooidc.Config{ClientID: clientID})

	return &OIDCProvider{
		oauth2Config: &oauth2Cfg,
		verifier:     verifier,
		TenantID:     tenantID,
		ClientID:     clientID,
	}, nil
}

// AuthCodeURL generates the Microsoft login URL with PKCE and state parameters.
// prompt=select_account forces the account picker even when a session is cached.
func (p *OIDCProvider) AuthCodeURL(state, codeChallenge string) string {
	return p.oauth2Config.AuthCodeURL(state,
		oauth2.S256ChallengeOption(codeChallenge),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

// Exchange trades the authorization code for tokens, verifying the ID token.
// Returns (idToken, accessToken, error).
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier string) (*gooidc.IDToken, string, error) {
	token, err := p.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return nil, "", fmt.Errorf("code exchange: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, "", fmt.Errorf("id_token missing from token response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, "", fmt.Errorf("id_token verification: %w", err)
	}

	return idToken, token.AccessToken, nil
}

// NewOIDCProviderFromRecord creates an OIDCProvider from a DB-stored SSOProvider record.
// The enc Encryptor is used to decrypt the client secret at load time.
func NewOIDCProviderFromRecord(ctx context.Context, record *store.SSOProvider, enc *Encryptor, redirectURI string) (*OIDCProvider, error) {
	plainSecret, err := enc.Decrypt(record.ClientSecretEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt client secret for provider %s: %w", record.ID, err)
	}

	goProvider, err := gooidc.NewProvider(ctx, record.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", record.IssuerURL, err)
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     record.ClientID,
		ClientSecret: plainSecret,
		RedirectURL:  redirectURI,
		Endpoint:     goProvider.Endpoint(),
		Scopes:       record.Scopes,
	}
	verifier := goProvider.Verifier(&gooidc.Config{ClientID: record.ClientID})

	return &OIDCProvider{
		oauth2Config: &oauth2Cfg,
		verifier:     verifier,
		ClientID:     record.ClientID,
	}, nil
}
type IDTokenClaims struct {
	Sub               string   `json:"sub"`                // provider_id (oid for Entra)
	OID               string   `json:"oid"`                // Entra object ID (preferred)
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"` // Entra fallback for email
	UniqueName        string   `json:"unique_name"`        // older Entra claim
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`             // group object IDs (present when app emits groupMembershipClaims)
}

// ExtractClaims parses the standard claims from a verified ID token.
func ExtractClaims(idToken *gooidc.IDToken) (*IDTokenClaims, error) {
	var claims IDTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("extract claims: %w", err)
	}
	// Entra uses oid as the stable identifier; fall back to sub
	if claims.OID != "" {
		claims.Sub = claims.OID
	}
	// Entra doesn't always populate the `email` claim even with the email scope.
	// Fall back to preferred_username (UPN), then unique_name.
	if claims.Email == "" {
		if claims.PreferredUsername != "" {
			claims.Email = claims.PreferredUsername
		} else if claims.UniqueName != "" {
			claims.Email = claims.UniqueName
		}
	}
	return &claims, nil
}
