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
