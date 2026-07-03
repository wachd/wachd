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

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Push token store tests ────────────────────────────────────────────────────

func TestDB_SavePushToken(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	userID := uuid.New()

	pt, err := db.SavePushToken(ctx, userID, "local", "test-device-token-1", "ios", team.ID)
	if err != nil {
		t.Fatalf("SavePushToken: %v", err)
	}
	if pt.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
	if pt.Token != "test-device-token-1" {
		t.Errorf("token: want %q, got %q", "test-device-token-1", pt.Token)
	}
	if pt.Platform != "ios" {
		t.Errorf("platform: want %q, got %q", "ios", pt.Platform)
	}
	if pt.UserID != userID {
		t.Errorf("user_id mismatch")
	}
	if pt.TeamID != team.ID {
		t.Errorf("team_id mismatch")
	}
}

func TestDB_SavePushToken_Upsert_ReassignsOwner(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	userA := uuid.New()
	userB := uuid.New()

	token := unique("apns-token")

	// Register under userA
	ptA, err := db.SavePushToken(ctx, userA, "local", token, "ios", team.ID)
	if err != nil {
		t.Fatalf("first SavePushToken: %v", err)
	}

	// Same token, different user (app reinstall scenario)
	ptB, err := db.SavePushToken(ctx, userB, "local", token, "android", team.ID)
	if err != nil {
		t.Fatalf("second SavePushToken: %v", err)
	}

	// ID should be the same (same row), but user and platform updated
	if ptA.ID != ptB.ID {
		t.Error("upsert should preserve the original row ID")
	}
	if ptB.UserID != userB {
		t.Errorf("user_id should be reassigned to userB")
	}
	if ptB.Platform != "android" {
		t.Errorf("platform should update on conflict: want android, got %s", ptB.Platform)
	}
}

func TestDB_GetPushTokensByUserID_MultiPlatform(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	userID := uuid.New()

	iosToken := unique("ios-tok")
	androidToken := unique("android-tok")

	if _, err := db.SavePushToken(ctx, userID, "local", iosToken, "ios", team.ID); err != nil {
		t.Fatalf("save iOS token: %v", err)
	}
	if _, err := db.SavePushToken(ctx, userID, "local", androidToken, "android", team.ID); err != nil {
		t.Fatalf("save Android token: %v", err)
	}

	tokens, err := db.GetPushTokensByUserID(ctx, userID, "local")
	if err != nil {
		t.Fatalf("GetPushTokensByUserID: %v", err)
	}

	if len(tokens) < 2 {
		t.Fatalf("expected at least 2 tokens, got %d", len(tokens))
	}

	platforms := map[string]bool{}
	for _, tok := range tokens {
		platforms[tok.Platform] = true
		if tok.UserID != userID {
			t.Errorf("token user_id mismatch: got %s, want %s", tok.UserID, userID)
		}
	}
	if !platforms["ios"] {
		t.Error("expected iOS token in results")
	}
	if !platforms["android"] {
		t.Error("expected Android token in results")
	}
}

func TestDB_GetPushTokensByUserID_OtherUserIsolation(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	userA := uuid.New()
	userB := uuid.New()

	if _, err := db.SavePushToken(ctx, userA, "local", unique("tok-a"), "ios", team.ID); err != nil {
		t.Fatalf("save userA token: %v", err)
	}

	// userB should not see userA's tokens
	tokens, err := db.GetPushTokensByUserID(ctx, userB, "local")
	if err != nil {
		t.Fatalf("GetPushTokensByUserID: %v", err)
	}
	for _, tok := range tokens {
		if tok.UserID == userA {
			t.Error("userB query returned a token belonging to userA — isolation breach")
		}
	}
}

func TestDB_DeletePushToken(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	userID := uuid.New()
	token := unique("del-tok")

	if _, err := db.SavePushToken(ctx, userID, "local", token, "ios", team.ID); err != nil {
		t.Fatalf("SavePushToken: %v", err)
	}

	if err := db.DeletePushToken(ctx, userID, "local", token); err != nil {
		t.Fatalf("DeletePushToken: %v", err)
	}

	// Second delete should return ErrNoRows (idempotent from caller's perspective)
	err := db.DeletePushToken(ctx, userID, "local", token)
	if err != pgx.ErrNoRows {
		t.Errorf("second delete: want pgx.ErrNoRows, got %v", err)
	}
}

func TestDB_DeletePushToken_EnforcesOwnership(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team := createTestTeamForPush(t, db, ctx)
	owner := uuid.New()
	attacker := uuid.New()
	token := unique("owned-tok")

	if _, err := db.SavePushToken(ctx, owner, "local", token, "ios", team.ID); err != nil {
		t.Fatalf("SavePushToken: %v", err)
	}

	// attacker tries to delete owner's token — must fail with ErrNoRows
	err := db.DeletePushToken(ctx, attacker, "local", token)
	if err != pgx.ErrNoRows {
		t.Errorf("cross-user delete: want pgx.ErrNoRows, got %v", err)
	}

	// Token must still exist (owned user can still delete it)
	tokens, err := db.GetPushTokensByUserID(ctx, owner, "local")
	if err != nil {
		t.Fatalf("GetPushTokensByUserID after failed cross-user delete: %v", err)
	}
	found := false
	for _, tok := range tokens {
		if tok.Token == token {
			found = true
		}
	}
	if !found {
		t.Error("token was deleted by the wrong user — ownership not enforced")
	}
}

// createTestTeamForPush creates a throwaway team and registers cleanup.
func createTestTeamForPush(t *testing.T, db *DB, ctx context.Context) *Team {
	t.Helper()
	team, err := db.CreateTeam(ctx, unique("push-team"), unique("push-secret"))
	if err != nil {
		t.Fatalf("CreateTeam for push test: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })
	return team
}
