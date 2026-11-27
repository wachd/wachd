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

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

// AccessSource is the part of the database a session is revalidated against.
// *store.DB satisfies it.
type AccessSource interface {
	GetLocalUserByID(ctx context.Context, id uuid.UUID) (*store.LocalUser, error)
	GetLocalUserTeams(ctx context.Context, userID uuid.UUID) ([]store.TeamAccess, error)
	GetIdentityTeams(ctx context.Context, identityID uuid.UUID) ([]store.TeamAccess, error)
}

var roleRank = map[string]int{"viewer": 1, "responder": 2, "admin": 3}

// mergeAccess collapses grants into team IDs and one role per team. When a user
// holds several grants for a team (several groups, or local plus SSO) the most
// permissive role wins.
func mergeAccess(grants []store.TeamAccess) ([]uuid.UUID, map[string]string) {
	roles := make(map[string]string, len(grants))
	ids := make([]uuid.UUID, 0, len(grants))
	for _, g := range grants {
		key := g.TeamID.String()
		cur, seen := roles[key]
		if !seen {
			ids = append(ids, g.TeamID)
		}
		if !seen || roleRank[g.Role] > roleRank[cur] {
			roles[key] = g.Role
		}
	}
	return ids, roles
}

// revalidateSession re-resolves a stored session's identity and access from the
// database. It returns (nil, nil) when access has been revoked: the user was
// deleted or deactivated, or an SSO identity no longer holds any team grant.
// A database error is returned as an error so callers fail closed.
func revalidateSession(ctx context.Context, src AccessSource, stored *Session) (*Session, error) {
	sess := *stored // never mutate what the caller loaded
	switch {
	case sess.LocalUserID != nil:
		user, err := src.GetLocalUserByID(ctx, *sess.LocalUserID)
		if err != nil {
			return nil, fmt.Errorf("revalidate local user: %w", err)
		}
		if user == nil || !user.IsActive {
			return nil, nil
		}
		grants, err := src.GetLocalUserTeams(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("revalidate local user teams: %w", err)
		}
		sess.TeamIDs, sess.Roles = mergeAccess(grants)
		sess.IsSuperAdmin = user.IsSuperAdmin
		sess.ForcePasswordChange = user.ForcePasswordChange
		sess.Email, sess.Name = user.Email, user.Name
	case sess.IdentityID != nil:
		grants, err := src.GetIdentityTeams(ctx, *sess.IdentityID)
		if err != nil {
			return nil, fmt.Errorf("revalidate sso identity: %w", err)
		}
		if len(grants) == 0 {
			return nil, nil // login rejects SSO users without a grant, so must requests
		}
		sess.TeamIDs, sess.Roles = mergeAccess(grants)
	default:
		return stored, nil // legacy session with no subject to look up
	}
	return &sess, nil
}
