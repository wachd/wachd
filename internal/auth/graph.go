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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

const graphMemberOfURL = "https://graph.microsoft.com/v1.0/me/memberOf?$select=id,displayName"

// GroupMembership represents one Entra group the user belongs to.
type GroupMembership struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// GetGroupMemberships calls Microsoft Graph to list the user's group memberships.
func GetGroupMemberships(ctx context.Context, accessToken string) ([]GroupMembership, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphMemberOfURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build graph request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		// Log the detail server-side; do not return raw Graph response to callers
		log.Printf("auth: graph API returned %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("graph API returned status %d", resp.StatusCode)
	}

	var result struct {
		Value []GroupMembership `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode graph response: %w", err)
	}

	return result.Value, nil
}

// GraphUser represents a user returned by the Graph groups/{id}/members endpoint.
type GraphUser struct {
	ID                string `json:"id"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

// TenantIDFromIssuerURL extracts the Azure tenant ID from a Microsoft OIDC issuer URL.
// Returns ("", false) for non-Entra issuers.
func TenantIDFromIssuerURL(issuerURL string) (string, bool) {
	const prefix = "https://login.microsoftonline.com/"
	if !strings.HasPrefix(issuerURL, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(issuerURL, prefix)
	tenantID, _, _ := strings.Cut(rest, "/")
	if tenantID == "" {
		return "", false
	}
	return tenantID, true
}

// EmailFromGraphUser returns the best available email for a Graph user.
// For guest accounts the mail field is often empty; the UPN carries the address
// in the form "alias_domain.com#EXT#@tenant" — this function recovers the
// original email from that format.
func EmailFromGraphUser(u GraphUser) string {
	if u.Mail != "" {
		return u.Mail
	}
	upn := u.UserPrincipalName
	if idx := strings.Index(upn, "#EXT#"); idx != -1 {
		// Guest UPN: "user_example.com#EXT#@tenant.onmicrosoft.com"
		// Recover "user@example.com" by replacing the last '_' before #EXT# with '@'
		local := upn[:idx]
		if sep := strings.LastIndex(local, "_"); sep != -1 {
			return local[:sep] + "@" + local[sep+1:]
		}
	}
	return upn
}

// GetGroupMembers fetches direct user members of an Entra group using app
// credentials (client credentials grant). Uses the type-cast URL
// /members/microsoft.graph.user so nested groups and service principals are
// excluded — they have no mail or UPN and would be silently skipped otherwise.
// Requires GroupMember.Read.All application permission with admin consent.
func GetGroupMembers(ctx context.Context, tenantID, clientID, clientSecret, groupID string) ([]GraphUser, error) {
	accessToken, err := getAppToken(ctx, tenantID, clientID, clientSecret)
	if err != nil {
		return nil, fmt.Errorf("get app token: %w", err)
	}

	apiURL := fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/groups/%s/members/microsoft.graph.user?$select=id,displayName,mail,userPrincipalName",
		groupID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		log.Printf("auth: graph members API returned %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("graph API returned status %d", resp.StatusCode)
	}

	var result struct {
		Value []GraphUser `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode graph response: %w", err)
	}
	return result.Value, nil
}

// getAppToken obtains an app-level access token via the client credentials grant.
func getAppToken(ctx context.Context, tenantID, clientID, clientSecret string) (string, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID)
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	return tok.AccessToken, nil
}
