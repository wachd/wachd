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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// sha256Hex returns the SHA-256 hex of the input — same format as production API tokens.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum) // 64 chars — matches CHAR(64) column
}

// ── Incidents (extended) ──────────────────────────────────────────────────────

func TestDB_AcknowledgeIncident(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	user, err := db.CreateLocalUser(ctx, unique("user"), unique("u")+"@test.example", "U",
		"$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	incident := &Incident{
		TeamID: team.ID, Title: "ack test", Severity: "high",
		Status: "open", Source: "grafana", AlertPayload: []byte(`{}`),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	if err := db.AcknowledgeIncident(ctx, team.ID, incident.ID, user.ID); err != nil {
		t.Fatalf("AcknowledgeIncident: %v", err)
	}

	got, _ := db.GetIncident(ctx, team.ID, incident.ID)
	if got.Status != "acknowledged" {
		t.Errorf("expected status 'acknowledged', got %q", got.Status)
	}
	if got.AcknowledgedAt == nil {
		t.Error("expected AcknowledgedAt to be set")
	}
	if got.AssignedTo == nil || *got.AssignedTo != user.ID {
		t.Errorf("expected AssignedTo=%v, got %v", user.ID, got.AssignedTo)
	}
}

func TestDB_Incident_ToResponse(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	incident := &Incident{
		TeamID: team.ID, Title: "resp test", Severity: "medium",
		Status: "open", Source: "grafana",
		AlertPayload: []byte(`{"alertname":"Test","severity":"high"}`),
	}
	if err := db.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	got, _ := db.GetIncident(ctx, team.ID, incident.ID)
	resp, err := got.ToResponse()
	if err != nil {
		t.Fatalf("ToResponse: %v", err)
	}
	if resp.ID != incident.ID {
		t.Errorf("expected ID %v, got %v", incident.ID, resp.ID)
	}
	if resp.AlertPayload == nil {
		t.Error("expected parsed AlertPayload, got nil")
	}
	if resp.AlertPayload["alertname"] != "Test" {
		t.Errorf("expected alertname=Test, got %v", resp.AlertPayload["alertname"])
	}
}

func TestDB_CountIncidentsThisMonth(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	before, err := db.CountIncidentsThisMonth(ctx)
	if err != nil {
		t.Fatalf("CountIncidentsThisMonth: %v", err)
	}

	_ = db.CreateIncident(ctx, &Incident{
		TeamID: team.ID, Title: "count test", Severity: "low",
		Status: "open", Source: "grafana", AlertPayload: []byte(`{}`),
	})

	after, err := db.CountIncidentsThisMonth(ctx)
	if err != nil {
		t.Fatalf("CountIncidentsThisMonth after: %v", err)
	}
	if after < before+1 {
		t.Errorf("expected count >= %d, got %d", before+1, after)
	}
}

// ── Legacy Users (on-call roster) ─────────────────────────────────────────────

func TestDB_User_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	phone := "+1234567890"
	u := &User{
		TeamID: team.ID,
		Name:   "Alice",
		Email:  unique("alice") + "@test.example",
		Phone:  &phone,
		Role:   "responder",
	}

	// Create
	if err := db.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == uuid.Nil {
		t.Error("expected non-nil ID after create")
	}

	// GetUser
	got, err := db.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("expected Name=Alice, got %q", got.Name)
	}
	if got.Phone == nil || *got.Phone != phone {
		t.Errorf("expected Phone=%q, got %v", phone, got.Phone)
	}

	// UpdateUser
	u.Name = "Alice Updated"
	if err := db.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	updated, _ := db.GetUser(ctx, u.ID)
	if updated.Name != "Alice Updated" {
		t.Errorf("expected updated name, got %q", updated.Name)
	}

	// GetTeamUsers
	users, err := db.GetTeamUsers(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeamUsers: %v", err)
	}
	found := false
	for _, tu := range users {
		if tu.ID == u.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("GetTeamUsers: created user not found")
	}

	// DeleteUser
	if err := db.DeleteUser(ctx, team.ID, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	users2, _ := db.GetTeamUsers(ctx, team.ID)
	for _, tu := range users2 {
		if tu.ID == u.ID {
			t.Error("user still present after delete")
		}
	}
}

// ── API Tokens ─────────────────────────────────────────────────────────────────

func TestDB_APIToken_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("tokuser"), unique("tok")+"@test.example", "T", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	tokenHash := sha256Hex(uuid.New().String())

	// CreateAPIToken
	tok, err := db.CreateAPIToken(ctx, user.ID, "ci-token", tokenHash, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if tok.ID == uuid.Nil {
		t.Error("expected non-nil token ID")
	}
	if tok.Name != "ci-token" {
		t.Errorf("expected name 'ci-token', got %q", tok.Name)
	}

	// ListAPITokensByUser
	tokens, err := db.ListAPITokensByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAPITokensByUser: %v", err)
	}
	if len(tokens) == 0 {
		t.Error("expected at least one token")
	}

	// GetAPITokenWithUser
	retTok, retUser, err := db.GetAPITokenWithUser(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetAPITokenWithUser: %v", err)
	}
	if retTok == nil || retUser == nil {
		t.Fatal("expected non-nil token and user")
	}
	if retTok.ID != tok.ID {
		t.Errorf("token ID mismatch: %v vs %v", retTok.ID, tok.ID)
	}
	if retUser.ID != user.ID {
		t.Errorf("user ID mismatch: %v vs %v", retUser.ID, user.ID)
	}
	if retTok.StoredHash != tokenHash {
		t.Errorf("stored hash mismatch")
	}

	// GetAPITokenWithUser — unknown hash
	noTok, noUser, err := db.GetAPITokenWithUser(ctx, "no-such-hash")
	if err != nil {
		t.Fatalf("GetAPITokenWithUser unknown: %v", err)
	}
	if noTok != nil || noUser != nil {
		t.Error("expected nil for unknown hash")
	}

	// TouchAPIToken
	if err := db.TouchAPIToken(ctx, tok.ID); err != nil {
		t.Fatalf("TouchAPIToken: %v", err)
	}
	touched, _ := db.ListAPITokensByUser(ctx, user.ID)
	var found *APIToken
	for i := range touched {
		if touched[i].ID == tok.ID {
			found = &touched[i]
			break
		}
	}
	if found == nil {
		t.Fatal("token not found after touch")
	}
	if found.LastUsedAt == nil {
		t.Error("expected LastUsedAt set after Touch")
	}

	// DeleteAPIToken
	if err := db.DeleteAPIToken(ctx, tok.ID, user.ID); err != nil {
		t.Fatalf("DeleteAPIToken: %v", err)
	}
	tokens2, _ := db.ListAPITokensByUser(ctx, user.ID)
	for _, t2 := range tokens2 {
		if t2.ID == tok.ID {
			t.Error("token still present after delete")
		}
	}
}

func TestDB_APIToken_WrongUser_Delete(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, _ := db.CreateLocalUser(ctx, unique("u"), unique("u")+"@test.example", "U", hash, false, false)
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	tok, _ := db.CreateAPIToken(ctx, user.ID, "test", "hash-"+uuid.New().String(), nil)

	// Delete with wrong user ID — should return pgx.ErrNoRows
	err := db.DeleteAPIToken(ctx, tok.ID, uuid.New())
	if err == nil {
		t.Error("expected error when deleting token with wrong user ID")
	}

	// Cleanup
	_ = db.DeleteAPIToken(ctx, tok.ID, user.ID)
}

// ── Local Groups ──────────────────────────────────────────────────────────────

func TestDB_LocalGroup_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	name := unique("group")

	// Create
	g, err := db.CreateLocalGroup(ctx, name, "test group")
	if err != nil {
		t.Fatalf("CreateLocalGroup: %v", err)
	}
	if g.ID == uuid.Nil {
		t.Error("expected non-nil group ID")
	}
	if g.Name != name {
		t.Errorf("expected name %q, got %q", name, g.Name)
	}

	// List
	groups, err := db.ListLocalGroups(ctx)
	if err != nil {
		t.Fatalf("ListLocalGroups: %v", err)
	}
	found := false
	for _, lg := range groups {
		if lg.ID == g.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListLocalGroups: created group not found")
	}

	// Delete
	if err := db.DeleteLocalGroup(ctx, g.ID); err != nil {
		t.Fatalf("DeleteLocalGroup: %v", err)
	}

	// Verify gone
	groups2, _ := db.ListLocalGroups(ctx)
	for _, lg := range groups2 {
		if lg.ID == g.ID {
			t.Error("group still present after delete")
		}
	}
}

func TestDB_LocalGroup_MemberManagement(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("guser"), unique("g")+"@test.example", "G", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	g, err := db.CreateLocalGroup(ctx, unique("mgroup"), "")
	if err != nil {
		t.Fatalf("CreateLocalGroup: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalGroup(ctx, g.ID) })

	// AddGroupMember
	if err := db.AddGroupMember(ctx, g.ID, user.ID); err != nil {
		t.Fatalf("AddGroupMember: %v", err)
	}

	// ListGroupMembers
	members, err := db.ListGroupMembers(ctx, g.ID)
	if err != nil {
		t.Fatalf("ListGroupMembers: %v", err)
	}
	if len(members) != 1 || members[0].ID != user.ID {
		t.Errorf("expected 1 member %v, got %v", user.ID, members)
	}

	// Idempotent add (ON CONFLICT DO NOTHING)
	if err := db.AddGroupMember(ctx, g.ID, user.ID); err != nil {
		t.Fatalf("AddGroupMember duplicate: %v", err)
	}
	members2, _ := db.ListGroupMembers(ctx, g.ID)
	if len(members2) != 1 {
		t.Errorf("expected 1 member after duplicate add, got %d", len(members2))
	}

	// RemoveGroupMember
	if err := db.RemoveGroupMember(ctx, g.ID, user.ID); err != nil {
		t.Fatalf("RemoveGroupMember: %v", err)
	}
	members3, _ := db.ListGroupMembers(ctx, g.ID)
	if len(members3) != 0 {
		t.Error("expected 0 members after remove")
	}
}

func TestDB_LocalGroup_AccessManagement(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	g, err := db.CreateLocalGroup(ctx, unique("agroup"), "")
	if err != nil {
		t.Fatalf("CreateLocalGroup: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalGroup(ctx, g.ID) })

	// GrantGroupAccess
	if err := db.GrantGroupAccess(ctx, g.ID, team.ID, "responder"); err != nil {
		t.Fatalf("GrantGroupAccess: %v", err)
	}

	// ListGroupAccess
	access, err := db.ListGroupAccess(ctx, g.ID)
	if err != nil {
		t.Fatalf("ListGroupAccess: %v", err)
	}
	if len(access) != 1 {
		t.Fatalf("expected 1 access entry, got %d", len(access))
	}
	if access[0].TeamID != team.ID {
		t.Errorf("expected teamID %v, got %v", team.ID, access[0].TeamID)
	}
	if access[0].Role != "responder" {
		t.Errorf("expected role 'responder', got %q", access[0].Role)
	}

	// GrantGroupAccess with upsert — upgrade role
	if err := db.GrantGroupAccess(ctx, g.ID, team.ID, "admin"); err != nil {
		t.Fatalf("GrantGroupAccess upsert: %v", err)
	}
	access2, _ := db.ListGroupAccess(ctx, g.ID)
	if len(access2) != 1 || access2[0].Role != "admin" {
		t.Errorf("expected role 'admin' after upsert, got %v", access2)
	}

	// RevokeGroupAccess
	if err := db.RevokeGroupAccess(ctx, g.ID, team.ID); err != nil {
		t.Fatalf("RevokeGroupAccess: %v", err)
	}
	access3, _ := db.ListGroupAccess(ctx, g.ID)
	if len(access3) != 0 {
		t.Error("expected 0 access entries after revoke")
	}
}

func TestDB_GetLocalUserTeams(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("lusr"), unique("lu")+"@test.example", "LU", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	g, err := db.CreateLocalGroup(ctx, unique("g"), "")
	if err != nil {
		t.Fatalf("CreateLocalGroup: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalGroup(ctx, g.ID) })

	_ = db.AddGroupMember(ctx, g.ID, user.ID)
	_ = db.GrantGroupAccess(ctx, g.ID, team.ID, "responder")

	teams, err := db.GetLocalUserTeams(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetLocalUserTeams: %v", err)
	}
	if len(teams) == 0 {
		t.Error("expected at least one team access")
	}
	found := false
	for _, ta := range teams {
		if ta.TeamID == team.ID {
			found = true
			if ta.Role != "responder" {
				t.Errorf("expected role 'responder', got %q", ta.Role)
			}
		}
	}
	if !found {
		t.Error("GetLocalUserTeams: expected team not found")
	}
}

// ── Schedules ─────────────────────────────────────────────────────────────────

func TestDB_Schedule_UpsertAndGet(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	s := &Schedule{
		TeamID:         team.ID,
		Name:           "Primary",
		RotationConfig: []byte(`{"rotation":[]}`),
		Enabled:        true,
	}

	// Upsert (insert)
	if err := db.UpsertSchedule(ctx, s); err != nil {
		t.Fatalf("UpsertSchedule (insert): %v", err)
	}
	if s.ID == uuid.Nil {
		t.Error("expected non-nil schedule ID after insert")
	}

	// GetSchedule
	got, err := db.GetSchedule(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got == nil || got.ID != s.ID {
		t.Errorf("GetSchedule: expected %v, got %v", s.ID, got)
	}

	// GetScheduleByID
	byID, err := db.GetScheduleByID(ctx, s.ID, team.ID)
	if err != nil {
		t.Fatalf("GetScheduleByID: %v", err)
	}
	if byID == nil || byID.ID != s.ID {
		t.Errorf("GetScheduleByID: expected %v, got %v", s.ID, byID)
	}

	// Upsert (update)
	s.Name = "Primary Updated"
	if err := db.UpsertSchedule(ctx, s); err != nil {
		t.Fatalf("UpsertSchedule (update): %v", err)
	}
	got2, _ := db.GetSchedule(ctx, team.ID)
	if got2.Name != "Primary Updated" {
		t.Errorf("expected updated name, got %q", got2.Name)
	}

	// GetScheduleForAPI
	resp, err := db.GetScheduleForAPI(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetScheduleForAPI: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil ScheduleResponse")
	}
	if resp.TeamID != team.ID {
		t.Errorf("expected teamID %v, got %v", team.ID, resp.TeamID)
	}
}

func TestDB_GetSchedule_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	got, err := db.GetSchedule(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for team with no schedule")
	}
}

func TestDB_GetScheduleForAPI_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	resp, err := db.GetScheduleForAPI(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Error("expected nil for team with no schedule")
	}
}

func TestDB_Schedule_Overrides(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("ovu"), unique("ov")+"@test.example", "OV", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	s := &Schedule{
		TeamID: team.ID, Name: "Sched",
		RotationConfig: []byte(`{}`), Enabled: true,
	}
	if err := db.UpsertSchedule(ctx, s); err != nil {
		t.Fatalf("UpsertSchedule: %v", err)
	}

	now := time.Now().UTC()
	reason := "vacation"
	override := &ScheduleOverride{
		ScheduleID: s.ID,
		TeamID:     team.ID,
		StartAt:    now.Add(-time.Hour),
		EndAt:      now.Add(time.Hour),
		UserID:     user.ID,
		Reason:     &reason,
		CreatedBy:  user.ID,
	}

	// CreateOverride
	if err := db.CreateOverride(ctx, override); err != nil {
		t.Fatalf("CreateOverride: %v", err)
	}
	if override.ID == uuid.Nil {
		t.Error("expected non-nil override ID")
	}

	// GetActiveOverrideForSchedule — at current time (within window)
	active, err := db.GetActiveOverrideForSchedule(ctx, s.ID, team.ID, now)
	if err != nil {
		t.Fatalf("GetActiveOverrideForSchedule: %v", err)
	}
	if active == nil {
		t.Fatal("expected active override, got nil")
	}
	if active.ID != override.ID {
		t.Errorf("expected override %v, got %v", override.ID, active.ID)
	}

	// GetActiveOverrideForSchedule — outside window
	past, err := db.GetActiveOverrideForSchedule(ctx, s.ID, team.ID, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("GetActiveOverrideForSchedule past: %v", err)
	}
	if past != nil {
		t.Error("expected nil for time outside override window")
	}

	// ListOverridesForSchedule
	overrides, err := db.ListOverridesForSchedule(ctx, s.ID, team.ID)
	if err != nil {
		t.Fatalf("ListOverridesForSchedule: %v", err)
	}
	if len(overrides) == 0 {
		t.Error("expected at least 1 override")
	}

	// ListOverridesForRange — overlapping
	rangeOvr, err := db.ListOverridesForRange(ctx, s.ID, team.ID, now.Add(-30*time.Minute), now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("ListOverridesForRange: %v", err)
	}
	if len(rangeOvr) == 0 {
		t.Error("expected override in range")
	}

	// ListOverridesForRange — non-overlapping
	noOvr, err := db.ListOverridesForRange(ctx, s.ID, team.ID, now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("ListOverridesForRange empty: %v", err)
	}
	if len(noOvr) != 0 {
		t.Errorf("expected 0 overrides for non-overlapping range, got %d", len(noOvr))
	}

	// DeleteOverride
	if err := db.DeleteOverride(ctx, override.ID, team.ID); err != nil {
		t.Fatalf("DeleteOverride: %v", err)
	}
	gone, err := db.GetActiveOverrideForSchedule(ctx, s.ID, team.ID, now)
	if err != nil {
		t.Fatalf("GetActiveOverrideForSchedule after delete: %v", err)
	}
	if gone != nil {
		t.Error("expected nil after override delete")
	}
}

func TestDB_EscalationPolicy_UpsertAndGet(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	// GetEscalationPolicy — not yet set
	nilP, err := db.GetEscalationPolicy(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetEscalationPolicy (not set): %v", err)
	}
	if nilP != nil {
		t.Error("expected nil policy for new team")
	}

	p := &EscalationPolicy{
		TeamID: team.ID,
		Config: []byte(`{"layers":[],"repeat_interval_minutes":30,"max_repeats":3}`),
	}

	// Upsert (insert)
	if err := db.UpsertEscalationPolicy(ctx, p); err != nil {
		t.Fatalf("UpsertEscalationPolicy: %v", err)
	}
	if p.ID == uuid.Nil {
		t.Error("expected non-nil policy ID")
	}

	// Get
	got, err := db.GetEscalationPolicy(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetEscalationPolicy: %v", err)
	}
	if got == nil || got.ID != p.ID {
		t.Errorf("expected policy %v, got %v", p.ID, got)
	}

	// Upsert (update) — change repeat_interval_minutes
	p.Config = []byte(`{"layers":[],"repeat_interval_minutes":60,"max_repeats":5}`)
	if err := db.UpsertEscalationPolicy(ctx, p); err != nil {
		t.Fatalf("UpsertEscalationPolicy (update): %v", err)
	}
	got2, _ := db.GetEscalationPolicy(ctx, team.ID)
	// JSONB normalizes key order — parse and compare field values instead of raw bytes
	var cfg2 map[string]interface{}
	if err := json.Unmarshal(got2.Config, &cfg2); err != nil {
		t.Fatalf("unmarshal updated config: %v", err)
	}
	if cfg2["repeat_interval_minutes"] != float64(60) {
		t.Errorf("expected repeat_interval_minutes=60, got %v", cfg2["repeat_interval_minutes"])
	}
	if cfg2["max_repeats"] != float64(5) {
		t.Errorf("expected max_repeats=5, got %v", cfg2["max_repeats"])
	}
}

// ── System Config ─────────────────────────────────────────────────────────────

func TestDB_SystemConfig_GetAndUpsert(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// GetSystemConfig — may return default if not seeded
	sc, err := db.GetSystemConfig(ctx)
	if err != nil {
		t.Fatalf("GetSystemConfig: %v", err)
	}
	if sc == nil {
		t.Fatal("expected non-nil system config")
	}
	// AIBackend should be "ollama" (default) or a previously set value
	if sc.AIBackend == "" {
		t.Error("expected non-empty AIBackend")
	}

	// SeedSystemConfig (idempotent — ON CONFLICT DO NOTHING)
	model := "llama3.2"
	if err := db.SeedSystemConfig(ctx, "ollama", &model); err != nil {
		t.Fatalf("SeedSystemConfig: %v", err)
	}

	// UpsertSystemConfig
	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	admin, err := db.CreateLocalUser(ctx, unique("syscfg"), unique("s")+"@test.example", "S", hash, true, false)
	if err != nil {
		t.Fatalf("CreateLocalUser (admin): %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, admin.ID) })

	newModel := "mistral"
	updated, err := db.UpsertSystemConfig(ctx, "ollama", &newModel, admin.ID)
	if err != nil {
		t.Fatalf("UpsertSystemConfig: %v", err)
	}
	if updated.AIBackend != "ollama" {
		t.Errorf("expected backend 'ollama', got %q", updated.AIBackend)
	}
	if updated.AIModel == nil || *updated.AIModel != "mistral" {
		t.Errorf("expected model 'mistral', got %v", updated.AIModel)
	}

	// Verify read-back
	readBack, _ := db.GetSystemConfig(ctx)
	if readBack.AIModel == nil || *readBack.AIModel != "mistral" {
		t.Errorf("read-back: expected model 'mistral', got %v", readBack.AIModel)
	}
}

// ── Team Config ───────────────────────────────────────────────────────────────

func TestDB_TeamConfig_GetAndUpsert(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	// GetTeamConfig — not yet configured
	nilCfg, err := db.GetTeamConfig(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeamConfig (not set): %v", err)
	}
	if nilCfg != nil {
		t.Error("expected nil config for new team")
	}

	channel := "#alerts"
	tc := &TeamConfig{
		TeamID:       team.ID,
		SlackChannel: &channel,
	}

	// UpsertTeamConfig (insert)
	if err := db.UpsertTeamConfig(ctx, tc); err != nil {
		t.Fatalf("UpsertTeamConfig (insert): %v", err)
	}

	// GetTeamConfig
	got, err := db.GetTeamConfig(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeamConfig: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if got.SlackChannel == nil || *got.SlackChannel != channel {
		t.Errorf("expected SlackChannel=%q, got %v", channel, got.SlackChannel)
	}

	// Upsert (update)
	newChannel := "#ops"
	tc.SlackChannel = &newChannel
	if err := db.UpsertTeamConfig(ctx, tc); err != nil {
		t.Fatalf("UpsertTeamConfig (update): %v", err)
	}
	got2, _ := db.GetTeamConfig(ctx, team.ID)
	if got2.SlackChannel == nil || *got2.SlackChannel != newChannel {
		t.Errorf("expected updated channel %q, got %v", newChannel, got2.SlackChannel)
	}
}

// ── Team Members ──────────────────────────────────────────────────────────────

func TestDB_GetTeamMembers_ViaGroupAccess(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("mem"), unique("m")+"@test.example", "Member", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	g, err := db.CreateLocalGroup(ctx, unique("g"), "")
	if err != nil {
		t.Fatalf("CreateLocalGroup: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalGroup(ctx, g.ID) })

	_ = db.AddGroupMember(ctx, g.ID, user.ID)
	_ = db.GrantGroupAccess(ctx, g.ID, team.ID, "responder")

	members, err := db.GetTeamMembers(ctx, team.ID)
	if err != nil {
		t.Fatalf("GetTeamMembers: %v", err)
	}

	found := false
	for _, m := range members {
		if m.ID == user.ID {
			found = true
			if m.Source != "local" {
				t.Errorf("expected source='local', got %q", m.Source)
			}
			if m.Role != "responder" {
				t.Errorf("expected role='responder', got %q", m.Role)
			}
		}
	}
	if !found {
		t.Error("GetTeamMembers: user not found in team members")
	}
}

func TestDB_GetMemberByID_Local(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("mbyid"), unique("mbi")+"@test.example", "MBI", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	m, err := db.GetMemberByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetMemberByID: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil member")
	}
	if m.Source != "local" {
		t.Errorf("expected source='local', got %q", m.Source)
	}
	if m.Email != user.Email {
		t.Errorf("expected email %q, got %q", user.Email, m.Email)
	}
}

func TestDB_GetMemberByID_NotFound(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	m, err := db.GetMemberByID(ctx, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Error("expected nil for non-existent member")
	}
}

func TestDB_UpdateMemberPhone_Local(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	hash := "$2a$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user, err := db.CreateLocalUser(ctx, unique("phone"), unique("ph")+"@test.example", "PH", hash, false, false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteLocalUser(ctx, user.ID) })

	phone := "+44123456789"
	if err := db.UpdateMemberPhone(ctx, user.ID, "local", &phone); err != nil {
		t.Fatalf("UpdateMemberPhone: %v", err)
	}

	updated, _ := db.GetLocalUserByID(ctx, user.ID)
	if updated.Phone == nil || *updated.Phone != phone {
		t.Errorf("expected phone %q, got %v", phone, updated.Phone)
	}
}

func TestDB_UpdateMemberPhone_InvalidSource(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	phone := "+1"
	err := db.UpdateMemberPhone(ctx, uuid.New(), "invalid-source", &phone)
	if err == nil {
		t.Error("expected error for invalid source")
	}
}

// ── db.Pool ───────────────────────────────────────────────────────────────────

func TestDB_Pool(t *testing.T) {
	db := requireDB(t)
	p := db.Pool()
	if p == nil {
		t.Error("expected non-nil pool")
	}
}

// ── SSO Providers ─────────────────────────────────────────────────────────────

func TestDB_SSOProvider_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	input := SSOProviderInput{
		Name:            unique("provider"),
		ProviderType:    "oidc",
		IssuerURL:       "https://login.example.com",
		ClientID:        "client-" + uuid.New().String()[:8],
		ClientSecretEnc: "enc-secret",
		Scopes:          []string{"openid", "email", "profile"},
		Enabled:         true,
		AutoProvision:   false,
	}

	// Create
	p, err := db.CreateSSOProvider(ctx, input)
	if err != nil {
		t.Fatalf("CreateSSOProvider: %v", err)
	}
	if p.ID == uuid.Nil {
		t.Error("expected non-nil provider ID")
	}
	t.Cleanup(func() { _ = db.DeleteSSOProvider(ctx, p.ID) })

	// GetSSOProvider
	got, err := db.GetSSOProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetSSOProvider: %v", err)
	}
	if got == nil || got.ID != p.ID {
		t.Errorf("expected provider %v, got %v", p.ID, got)
	}
	if got.Name != input.Name {
		t.Errorf("expected name %q, got %q", input.Name, got.Name)
	}

	// GetSSOProvider — not found
	nilP, err := db.GetSSOProvider(ctx, uuid.New())
	if err != nil {
		t.Fatalf("GetSSOProvider not found: %v", err)
	}
	if nilP != nil {
		t.Error("expected nil for unknown provider ID")
	}

	// ListSSOProviders — all
	all, err := db.ListSSOProviders(ctx, false)
	if err != nil {
		t.Fatalf("ListSSOProviders (all): %v", err)
	}
	foundAll := false
	for _, lp := range all {
		if lp.ID == p.ID {
			foundAll = true
			break
		}
	}
	if !foundAll {
		t.Error("ListSSOProviders (all): provider not found")
	}

	// ListSSOProviders — enabled only
	enabled, err := db.ListSSOProviders(ctx, true)
	if err != nil {
		t.Fatalf("ListSSOProviders (enabled): %v", err)
	}
	foundEnabled := false
	for _, lp := range enabled {
		if lp.ID == p.ID {
			foundEnabled = true
			break
		}
	}
	if !foundEnabled {
		t.Error("ListSSOProviders (enabled): enabled provider not found")
	}

	// UpdateSSOProvider — change name
	newName := unique("updated-provider")
	updated, err := db.UpdateSSOProvider(ctx, p.ID, SSOProviderUpdate{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateSSOProvider: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("expected name %q, got %q", newName, updated.Name)
	}

	// UpdateSSOProvider — no fields (should return current)
	noChange, err := db.UpdateSSOProvider(ctx, p.ID, SSOProviderUpdate{})
	if err != nil {
		t.Fatalf("UpdateSSOProvider no fields: %v", err)
	}
	if noChange == nil {
		t.Error("expected non-nil on no-op update")
	}

	// DeleteSSOProvider
	if err := db.DeleteSSOProvider(ctx, p.ID); err != nil {
		t.Fatalf("DeleteSSOProvider: %v", err)
	}

	// Verify gone
	gone, err := db.GetSSOProvider(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetSSOProvider after delete: %v", err)
	}
	if gone != nil {
		t.Error("expected nil after delete")
	}
}

// ── SSO Identities and Group Mappings ─────────────────────────────────────────

func TestDB_UpsertSSOIdentity(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	provider := "entra"
	providerID := uuid.New().String()
	email := unique("sso") + "@sso.example"
	name := "SSO User"

	// Insert
	id, err := db.UpsertSSOIdentity(ctx, provider, providerID, email, name, nil)
	if err != nil {
		t.Fatalf("UpsertSSOIdentity (insert): %v", err)
	}
	if id.ID == uuid.Nil {
		t.Error("expected non-nil identity ID")
	}
	if id.Email != email {
		t.Errorf("expected email %q, got %q", email, id.Email)
	}

	// Upsert (update email)
	newEmail := unique("updated-sso") + "@sso.example"
	id2, err := db.UpsertSSOIdentity(ctx, provider, providerID, newEmail, "SSO User Updated", nil)
	if err != nil {
		t.Fatalf("UpsertSSOIdentity (update): %v", err)
	}
	if id2.ID != id.ID {
		t.Errorf("expected same ID on upsert, got %v vs %v", id.ID, id2.ID)
	}
	if id2.Email != newEmail {
		t.Errorf("expected updated email %q, got %q", newEmail, id2.Email)
	}
}

func TestDB_GroupMapping_CRUD(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	// Create SSO provider for the mapping
	p, err := db.CreateSSOProvider(ctx, SSOProviderInput{
		Name: unique("p"), ProviderType: "oidc",
		IssuerURL: "https://login.example.com",
		ClientID:  "c-" + uuid.New().String()[:8],
		Scopes:    []string{"openid"},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateSSOProvider: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteSSOProvider(ctx, p.ID) })

	groupName := "engineering"
	groupID := "grp-" + uuid.New().String()[:8]

	// CreateGroupMapping
	m, err := db.CreateGroupMapping(ctx, p.ID, groupID, &groupName, team.ID, "responder")
	if err != nil {
		t.Fatalf("CreateGroupMapping: %v", err)
	}
	if m.ID == uuid.Nil {
		t.Error("expected non-nil mapping ID")
	}

	// GetGroupMappings (by provider name — retrieved from the SSO provider)
	mappings, err := db.GetGroupMappings(ctx, m.Provider)
	if err != nil {
		t.Fatalf("GetGroupMappings: %v", err)
	}
	found := false
	for _, gm := range mappings {
		if gm.ID == m.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("GetGroupMappings: created mapping not found")
	}

	// ListGroupMappings
	all, err := db.ListGroupMappings(ctx)
	if err != nil {
		t.Fatalf("ListGroupMappings: %v", err)
	}
	foundAll := false
	for _, gm := range all {
		if gm.ID == m.ID {
			foundAll = true
			break
		}
	}
	if !foundAll {
		t.Error("ListGroupMappings: created mapping not found")
	}

	// DeleteGroupMapping
	if err := db.DeleteGroupMapping(ctx, m.ID); err != nil {
		t.Fatalf("DeleteGroupMapping: %v", err)
	}

	// Verify gone
	mappings2, _ := db.GetGroupMappings(ctx, m.Provider)
	for _, gm := range mappings2 {
		if gm.ID == m.ID {
			t.Error("mapping still present after delete")
		}
	}
}

func TestDB_EnsureGroupMappingBootstrap(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	groupName := "bootstrap-group"
	groupID := "boot-" + uuid.New().String()[:8]

	// First call — inserts
	if err := db.EnsureGroupMappingBootstrap(ctx, "entra", groupID, &groupName, team.ID, "viewer"); err != nil {
		t.Fatalf("EnsureGroupMappingBootstrap: %v", err)
	}

	// Second call — ON CONFLICT DO NOTHING, no error
	if err := db.EnsureGroupMappingBootstrap(ctx, "entra", groupID, &groupName, team.ID, "admin"); err != nil {
		t.Fatalf("EnsureGroupMappingBootstrap (duplicate): %v", err)
	}

	// Verify original role preserved (ON CONFLICT DO NOTHING = no update)
	mappings, _ := db.GetGroupMappings(ctx, "entra")
	for _, gm := range mappings {
		if gm.GroupID == groupID {
			if gm.Role != "viewer" {
				t.Errorf("expected role 'viewer' preserved, got %q", gm.Role)
			}
			// Cleanup
			_ = db.DeleteGroupMapping(ctx, gm.ID)
			break
		}
	}
}

// ── SSO Sessions ──────────────────────────────────────────────────────────────

func TestDB_Session_RecordAndDelete(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// sessions.identity_id references sso_identities (not local_users)
	identity, err := db.UpsertSSOIdentity(ctx, "entra", uuid.New().String(), unique("sess")+"@sso.example", "Sess", nil)
	if err != nil {
		t.Fatalf("UpsertSSOIdentity: %v", err)
	}

	sessionHash := sha256Hex("session-" + uuid.New().String())
	expiresAt := time.Now().Add(24 * time.Hour)

	// RecordSession
	if err := db.RecordSession(ctx, identity.ID, sessionHash, expiresAt, "127.0.0.1"); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}

	// Idempotent (ON CONFLICT DO NOTHING)
	if err := db.RecordSession(ctx, identity.ID, sessionHash, expiresAt, "10.0.0.1"); err != nil {
		t.Fatalf("RecordSession duplicate: %v", err)
	}

	// DeleteSessionByHash
	if err := db.DeleteSessionByHash(ctx, sessionHash); err != nil {
		t.Fatalf("DeleteSessionByHash: %v", err)
	}

	// Delete non-existent — no error (plain DELETE, no rows-affected check)
	if err := db.DeleteSessionByHash(ctx, sha256Hex("no-such-session")); err != nil {
		t.Fatalf("DeleteSessionByHash non-existent: %v", err)
	}
}

// ── SyncTeamAccess ────────────────────────────────────────────────────────────

func TestDB_SyncTeamAccess_EmptyGroups(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	// UpsertSSOIdentity to get a real identity ID
	identity, err := db.UpsertSSOIdentity(ctx, "entra", uuid.New().String(), unique("sync")+"@sso.example", "Sync User", nil)
	if err != nil {
		t.Fatalf("UpsertSSOIdentity: %v", err)
	}

	// SyncTeamAccess with empty groups — should revoke all (no-op since there's nothing yet)
	if err := db.SyncTeamAccess(ctx, identity.ID, []string{}, uuid.New()); err != nil {
		t.Fatalf("SyncTeamAccess empty: %v", err)
	}
}

func TestDB_GetIdentityTeams_Empty(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	teams, err := db.GetIdentityTeams(ctx, uuid.New())
	if err != nil {
		t.Fatalf("GetIdentityTeams: %v", err)
	}
	if len(teams) != 0 {
		t.Errorf("expected 0 teams for unknown identity, got %d", len(teams))
	}
}

func TestDB_SyncTeamAccess_WithGroupMapping(t *testing.T) {
	db := requireDB(t)
	ctx := context.Background()

	team, err := db.CreateTeam(ctx, unique("team"), unique("secret"))
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteTeam(ctx, team.ID) })

	// Create SSO provider
	provider, err := db.CreateSSOProvider(ctx, SSOProviderInput{
		Name: unique("sync-prov"), ProviderType: "oidc",
		IssuerURL: "https://login.example.com",
		ClientID:  "c-" + uuid.New().String()[:8],
		Scopes:    []string{"openid"},
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("CreateSSOProvider: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteSSOProvider(ctx, provider.ID) })

	// Create group mapping
	groupID := "g-" + uuid.New().String()[:8]
	m, err := db.CreateGroupMapping(ctx, provider.ID, groupID, nil, team.ID, "responder")
	if err != nil {
		t.Fatalf("CreateGroupMapping: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteGroupMapping(ctx, m.ID) })

	// Create SSO identity
	identity, err := db.UpsertSSOIdentity(ctx, "oidc", uuid.New().String(), unique("idn")+"@sso.example", "IDN", nil)
	if err != nil {
		t.Fatalf("UpsertSSOIdentity: %v", err)
	}

	// SyncTeamAccess with matching group
	if err := db.SyncTeamAccess(ctx, identity.ID, []string{groupID}, provider.ID); err != nil {
		t.Fatalf("SyncTeamAccess with group: %v", err)
	}

	// GetIdentityTeams — should now include the team
	teams, err := db.GetIdentityTeams(ctx, identity.ID)
	if err != nil {
		t.Fatalf("GetIdentityTeams: %v", err)
	}
	found := false
	for _, ta := range teams {
		if ta.TeamID == team.ID {
			found = true
		}
	}
	if !found {
		t.Error("GetIdentityTeams: expected team after SyncTeamAccess")
	}

	// SyncTeamAccess with empty groups — revoke all
	if err := db.SyncTeamAccess(ctx, identity.ID, []string{}, provider.ID); err != nil {
		t.Fatalf("SyncTeamAccess revoke: %v", err)
	}
	teams2, _ := db.GetIdentityTeams(ctx, identity.ID)
	for _, ta := range teams2 {
		if ta.TeamID == team.ID {
			t.Error("team access still present after SyncTeamAccess with empty groups")
		}
	}
}
