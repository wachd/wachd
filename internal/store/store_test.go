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
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
)

// requireDB connects to the test database and runs migrations.
// Skips the test if the database is not available.
func requireDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wachd:wachd_dev_password@localhost:5432/wachd"
	}
	db, err := NewDB(dsn)
	if err != nil {
		t.Skipf("skipping integration test: DB unavailable (%v) — run make docker-up", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// unique returns a sufficiently unique string for test fixture names.
func unique(prefix string) string {
	return fmt.Sprintf("%s_%s", prefix, uuid.New().String()[:8])
}

// ── Teams ─────────────────────────────────────────────────────────────────────

func TestDB_Team_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// Create
	name := unique("team")
	secret := unique("secret")
	team, err := db.CreateTeam(ctx, name, secret)
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	if team.ID == uuid.Nil {
		t.Error("expected non-nil team ID")
	}
	if team.Name != name {
		t.Errorf("expected name %q, got %q", name, team.Name)
	}
	t.Cleanup(func() {
		_ = db.DeleteTeam(ctx, team.ID)
	})

	// GetTeam
	got, err := db.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got == nil || got.ID != team.ID {
		t.Errorf("GetTeam: expected team %v, got %v", team.ID, got)
	}

	// GetTeamByWebhookSecret
	bySecret, err := db.GetTeamByWebhookSecret(ctx, secret)
	if err != nil {
		t.Fatalf("GetTeamByWebhookSecret: %v", err)
	}
	if bySecret == nil || bySecret.ID != team.ID {
		t.Errorf("GetTeamByWebhookSecret: expected team %v, got %v", team.ID, bySecret)
	}

	// ListTeams — verify our team is in the list
	teams, err := db.ListTeams(ctx)
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	found := false
	for _, tt := range teams {
		if tt.ID == team.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListTeams: created team not found in list")
	}

	// DeleteTeam
	if err := db.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatalf("DeleteTeam: %v", err)
	}

	// Verify deleted
	gone, err := db.GetTeam(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeam after delete: %v", err)
	}
	if gone != nil {
		t.Error("expected nil after deletion")
	}
}

func TestDB_GetTeam_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	got, err := db.GetTeam(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent team")
	}
}

func TestDB_GetTeamByWebhookSecret_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	got, err := db.GetTeamByWebhookSecret(ctx, "no-such-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for unknown webhook secret")
	}
}

func TestDB_GetOrCreateTeamByName(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	name := unique("team")
	team1, err := db.GetOrCreateTeamByName(ctx, name)
	if err != nil {
		t.Fatalf("GetOrCreateTeamByName (create): %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team1.ID) })

	// Second call should return the same team
	team2, err := db.GetOrCreateTeamByName(ctx, name)
	if err != nil {
		t.Fatalf("GetOrCreateTeamByName (get): %v", err)
	}
	if team1.ID != team2.ID {
		t.Errorf("expected same team ID: %v vs %v", team1.ID, team2.ID)
	}
}

func TestDB_CountTeams(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	before, err := db.CountTeams(ctx)
	if err != nil {
		t.Fatalf("CountTeams: %v", err)
	}

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	after, err := db.CountTeams(ctx)
	if err != nil {
		t.Fatalf("CountTeams after create: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected count %d, got %d", before+1, after)
	}
}

// ── Local Users ───────────────────────────────────────────────────────────────

func TestDB_LocalUser_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	username := unique("user")
	email := unique("user") + "@test.example"

	// Create
	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, username, email, "Test User", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if user.ID == uuid.Nil {
		t.Error("expected non-nil user ID")
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	// GetLocalUserByUsername
	byUsername, err := db.GetLocalUserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("GetLocalUserByUsername: %v", err)
	}
	if byUsername == nil || byUsername.ID != user.ID {
		t.Errorf("GetLocalUserByUsername: expected user %v, got %v", user.ID, byUsername)
	}

	// GetLocalUserByID
	byID, err := db.GetLocalUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetLocalUserByID: %v", err)
	}
	if byID == nil || byID.Username != username {
		t.Errorf("GetLocalUserByID: expected %q, got %v", username, byID)
	}

	// ListLocalUsers
	users, err := db.ListLocalUsers(ctx)
	if err != nil {
		t.Fatalf("ListLocalUsers: %v", err)
	}
	found := false
	for _, u := range users {
		if u.ID == user.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListLocalUsers: created user not found")
	}

	// UpdateLocalUser
	newEmail := unique("updated") + "@test.example"
	newName := "Updated Name"
	updated, err := db.UpdateLocalUser(ctx, user.ID, LocalUserUpdate{
		Email: &newEmail,
		Name:  &newName,
	})
	if err != nil {
		t.Fatalf("UpdateLocalUser: %v", err)
	}
	if updated.Email != newEmail {
		t.Errorf("expected email %q, got %q", newEmail, updated.Email)
	}
	if updated.Name != newName {
		t.Errorf("expected name %q, got %q", newName, updated.Name)
	}

	// DeleteLocalUser
	if err := db.DeleteLocalUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteLocalUser: %v", err)
	}
	gone, _ := db.GetLocalUserByID(ctx, user.ID)
	if gone != nil {
		t.Error("expected nil after deletion")
	}
}

func TestDB_LocalUser_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	u, err := db.GetLocalUserByUsername(ctx, "no-such-user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u != nil {
		t.Error("expected nil for non-existent user")
	}

	u2, err := db.GetLocalUserByID(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u2 != nil {
		t.Error("expected nil for non-existent user ID")
	}
}

func TestDB_LocalUser_CountAndSuperAdmin(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	before, err := db.CountLocalUsers(ctx)
	if err != nil {
		t.Fatalf("CountLocalUsers: %v", err)
	}

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("admin"), unique("admin")+"@test.example", "Admin", hash, true, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	if !user.IsSuperAdmin {
		t.Error("expected is_superadmin=true")
	}

	after, err := db.CountLocalUsers(ctx)
	if err != nil {
		t.Fatalf("CountLocalUsers: %v", err)
	}
	if after != before+1 {
		t.Errorf("expected count %d, got %d", before+1, after)
	}
}

func TestDB_LocalUser_UpdatePasswordHash(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("user"), unique("u")+"@test.example", "U", hash, false, true)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	newHash := "$2a$12$bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := db.UpdatePasswordHash(ctx, user.ID, newHash, false); err != nil {
		t.Fatalf("UpdatePasswordHash: %v", err)
	}

	updated, _ := db.GetLocalUserByID(ctx, user.ID)
	if updated.PasswordHash != newHash {
		t.Errorf("password hash not updated")
	}
	if updated.ForcePasswordChange {
		t.Error("force_password_change should be cleared")
	}
}

func TestDB_LocalUser_FailedAttempts(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("user"), unique("u")+"@test.example", "U", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	// Increment once
	if err := db.IncrementFailedAttempts(ctx, user.ID, nil); err != nil {
		t.Fatalf("IncrementFailedAttempts: %v", err)
	}
	u, _ := db.GetLocalUserByID(ctx, user.ID)
	if u.FailedLoginAttempts != 1 {
		t.Errorf("expected 1 failed attempt, got %d", u.FailedLoginAttempts)
	}

	// Increment again
	if err := db.IncrementFailedAttempts(ctx, user.ID, nil); err != nil {
		t.Fatalf("IncrementFailedAttempts: %v", err)
	}
	u, _ = db.GetLocalUserByID(ctx, user.ID)
	if u.FailedLoginAttempts != 2 {
		t.Errorf("expected 2 failed attempts, got %d", u.FailedLoginAttempts)
	}

	// Reset
	if err := db.ResetFailedAttempts(ctx, user.ID); err != nil {
		t.Fatalf("ResetFailedAttempts: %v", err)
	}
	u, _ = db.GetLocalUserByID(ctx, user.ID)
	if u.FailedLoginAttempts != 0 {
		t.Errorf("expected 0 failed attempts after reset, got %d", u.FailedLoginAttempts)
	}
}

func TestDB_LocalUser_RecordLogin(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("user"), unique("u")+"@test.example", "U", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	if user.LastLoginAt != nil {
		t.Error("expected nil LastLoginAt on new user")
	}

	if err := db.RecordLocalLogin(ctx, user.ID); err != nil {
		t.Fatalf("RecordLocalLogin: %v", err)
	}
	updated, _ := db.GetLocalUserByID(ctx, user.ID)
	if updated.LastLoginAt == nil {
		t.Error("expected LastLoginAt to be set after login")
	}
}

// ── Incidents ─────────────────────────────────────────────────────────────────

func TestDB_Incident_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// Need a team first
	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	// Create incident
	incident := &Incident{
		TeamID:   team.ID,
		Title:    "Test Alert",
		Severity: "high",
		Status:   "open",
		Source:   "grafana",
		AlertPayload: []byte(`{"alertname":"TestAlert"}`),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	if incident.ID == uuid.Nil {
		t.Error("expected non-nil incident ID after create")
	}

	// GetIncident — with correct team_id (tenant isolation)
	got, err := db.GetIncident(ctx, team.ID, incident.ID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if got == nil || got.Title != "Test Alert" {
		t.Errorf("GetIncident: expected 'Test Alert', got %v", got)
	}

	// GetIncident — with wrong team_id (tenant isolation must block)
	wrong, err := db.GetIncident(ctx, uuid.New(), incident.ID)
	if err != nil {
		t.Fatalf("GetIncident wrong team: %v", err)
	}
	if wrong != nil {
		t.Error("tenant isolation failed: incident returned for wrong team_id")
	}

	// ListIncidents
	list, err := db.ListIncidents(ctx, team.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(list) == 0 {
		t.Error("ListIncidents: expected at least 1 incident")
	}

	// UpdateIncidentStatus
	if err := db.UpdateIncidentStatus(ctx, team.ID, incident.ID, "acknowledged"); err != nil {
		t.Fatalf("UpdateIncidentStatus: %v", err)
	}
	acked, _ := db.GetIncident(ctx, team.ID, incident.ID)
	if acked.Status != "acknowledged" {
		t.Errorf("expected status 'acknowledged', got %q", acked.Status)
	}
}

func TestDB_Incident_TenantIsolation_List(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// Create two separate teams
	teamA, _ := db.CreateTeam(ctx, unique("teamA"), unique("secretA"))
	teamB, _ := db.CreateTeam(ctx, unique("teamB"), unique("secretB"))
	t.Cleanup(func() {
		_ = db.DeleteTeam(ctx, teamA.ID)
		_ = db.DeleteTeam(ctx, teamB.ID)
	})

	// Create incident for team A
	_ = db.CreateIncident(ctx, &Incident{
		TeamID:       teamA.ID,
		Title:        "Team A incident",
		Severity:     "high",
		Status:       "open",
		Source:       "grafana",
		AlertPayload: []byte(`{}`),
	})

	// List for team B — must not include team A's incident
	listB, err := db.ListIncidents(ctx, teamB.ID, 100, 0)
	if err != nil {
		t.Fatalf("ListIncidents teamB: %v", err)
	}
	for _, inc := range listB {
		if inc.TeamID == teamA.ID {
			t.Error("tenant isolation failed: team A incident appeared in team B list")
		}
	}
}

// ── Password Policy ───────────────────────────────────────────────────────────

func TestDB_PasswordPolicy_GetAndUpdate(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// Get default policy
	policy, err := db.GetPasswordPolicy(ctx)
	if err != nil {
		t.Fatalf("GetPasswordPolicy: %v", err)
	}
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}

	// Save original min_length for cleanup
	origLen := policy.MinLength

	// Update min_length
	newLen := origLen + 2
	updated, err := db.UpdatePasswordPolicy(ctx, PasswordPolicyUpdate{MinLength: &newLen})
	if err != nil {
		t.Fatalf("UpdatePasswordPolicy: %v", err)
	}
	if updated.MinLength != newLen {
		t.Errorf("expected min_length %d, got %d", newLen, updated.MinLength)
	}

	// Restore
	t.Cleanup(func() {
		_, _ = db.UpdatePasswordPolicy(ctx, PasswordPolicyUpdate{MinLength: &origLen})
	})

	// Verify read-back
	check, _ := db.GetPasswordPolicy(ctx)
	if check.MinLength != newLen {
		t.Errorf("read-back: expected %d, got %d", newLen, check.MinLength)
	}
}

func TestDB_PasswordPolicy_UpdateNoFields(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// Empty update should return current policy without error
	policy, err := db.UpdatePasswordPolicy(ctx, PasswordPolicyUpdate{})
	if err != nil {
		t.Fatalf("UpdatePasswordPolicy (no fields): %v", err)
	}
	if policy == nil {
		t.Error("expected non-nil policy on no-op update")
	}
}
