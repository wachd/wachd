package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

type fakeAccess struct {
	user     *store.LocalUser
	local    []store.TeamAccess
	identity []store.TeamAccess
	err      error
}

func (f *fakeAccess) GetLocalUserByID(context.Context, uuid.UUID) (*store.LocalUser, error) {
	return f.user, f.err
}
func (f *fakeAccess) GetLocalUserTeams(context.Context, uuid.UUID) ([]store.TeamAccess, error) {
	return f.local, f.err
}
func (f *fakeAccess) GetIdentityTeams(context.Context, uuid.UUID) ([]store.TeamAccess, error) {
	return f.identity, f.err
}

func localSession(uid, team uuid.UUID, role string) *Session {
	return &Session{
		LocalUserID: &uid,
		TeamIDs:     []uuid.UUID{team},
		Roles:       map[string]string{team.String(): role},
	}
}

// D11: a deleted or deactivated user must lose an existing session.
func TestRevalidate_DeletedOrInactiveUserIsRevoked(t *testing.T) {
	uid, team := uuid.New(), uuid.New()
	stored := localSession(uid, team, "admin")

	got, err := revalidateSession(context.Background(), &fakeAccess{user: nil}, stored)
	if err != nil || got != nil {
		t.Fatalf("deleted user: got %+v, %v; want revoked", got, err)
	}
	got, err = revalidateSession(context.Background(), &fakeAccess{user: &store.LocalUser{ID: uid, IsActive: false}}, stored)
	if err != nil || got != nil {
		t.Fatalf("inactive user: got %+v, %v; want revoked", got, err)
	}
}

// D11: removed access and lowered roles apply on the next request, and the
// stored snapshot is never mutated.
func TestRevalidate_RolesComeFromDatabase(t *testing.T) {
	uid, team, other := uuid.New(), uuid.New(), uuid.New()
	stored := localSession(uid, team, "admin")
	src := &fakeAccess{
		user:  &store.LocalUser{ID: uid, IsActive: true},
		local: []store.TeamAccess{{TeamID: other, Role: "viewer"}},
	}
	got, err := revalidateSession(context.Background(), src, stored)
	if err != nil || got == nil {
		t.Fatalf("unexpected: %+v, %v", got, err)
	}
	if got.HasTeamAccess(team) {
		t.Error("access to the removed team must be gone")
	}
	if !got.HasTeamAccess(other) || got.Roles[other.String()] != "viewer" {
		t.Errorf("new grant not applied: %+v", got.Roles)
	}
	if stored.Roles[team.String()] != "admin" {
		t.Error("stored session was mutated")
	}
}

func TestRevalidate_SuperAdminAndForceChangeFollowDatabase(t *testing.T) {
	uid, team := uuid.New(), uuid.New()
	stored := localSession(uid, team, "admin")
	stored.IsSuperAdmin = true
	src := &fakeAccess{user: &store.LocalUser{ID: uid, IsActive: true, IsSuperAdmin: false, ForcePasswordChange: true}}
	got, _ := revalidateSession(context.Background(), src, stored)
	if got == nil || got.IsSuperAdmin || !got.ForcePasswordChange {
		t.Fatalf("flags not refreshed: %+v", got)
	}
}

func TestRevalidate_MostPermissiveRoleWins(t *testing.T) {
	uid, team := uuid.New(), uuid.New()
	src := &fakeAccess{
		user: &store.LocalUser{ID: uid, IsActive: true},
		local: []store.TeamAccess{
			{TeamID: team, Role: "admin"},
			{TeamID: team, Role: "viewer"}, // listed last must not win
		},
	}
	got, _ := revalidateSession(context.Background(), src, localSession(uid, team, "viewer"))
	if got.Roles[team.String()] != "admin" || len(got.TeamIDs) != 1 {
		t.Fatalf("roles = %v ids = %v", got.Roles, got.TeamIDs)
	}
}

func TestRevalidate_SSOWithoutGrantsIsRevoked(t *testing.T) {
	id, team := uuid.New(), uuid.New()
	stored := &Session{IdentityID: &id, TeamIDs: []uuid.UUID{team}, Roles: map[string]string{team.String(): "admin"}}
	if got, err := revalidateSession(context.Background(), &fakeAccess{}, stored); got != nil || err != nil {
		t.Fatalf("got %+v, %v; want revoked", got, err)
	}
	src := &fakeAccess{identity: []store.TeamAccess{{TeamID: team, Role: "viewer"}}}
	got, _ := revalidateSession(context.Background(), src, stored)
	if got == nil || got.Roles[team.String()] != "viewer" {
		t.Fatalf("SSO role not refreshed: %+v", got)
	}
}

// Failing to verify access must deny, not fall back to the snapshot.
func TestRevalidate_DatabaseErrorFailsClosed(t *testing.T) {
	uid, team := uuid.New(), uuid.New()
	got, err := revalidateSession(context.Background(), &fakeAccess{err: errors.New("db down")}, localSession(uid, team, "admin"))
	if err == nil || got != nil {
		t.Fatalf("got %+v, %v; want error", got, err)
	}
}

// Sessions with no subject (legacy) keep working unchanged.
func TestRevalidate_LegacySessionPassesThrough(t *testing.T) {
	stored := &Session{Email: "a@b", TeamIDs: []uuid.UUID{uuid.New()}}
	got, err := revalidateSession(context.Background(), &fakeAccess{}, stored)
	if err != nil || got != stored {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// End to end through Redis: a revoked session is deleted and stays gone.
func TestSessionStoreLoad_RevokedSessionIsDeleted(t *testing.T) {
	sessions := requireAuthSessions(t)
	ctx := context.Background()
	uid, team := uuid.New(), uuid.New()
	token, err := sessions.Create(ctx, localSession(uid, team, "admin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Delete(ctx, token) })

	sessions.WithAccessSource(&fakeAccess{user: &store.LocalUser{ID: uid, IsActive: true},
		local: []store.TeamAccess{{TeamID: team, Role: "viewer"}}})
	got, err := sessions.Load(ctx, token)
	if err != nil || got == nil || got.Roles[team.String()] != "viewer" {
		t.Fatalf("downgrade not applied: %+v, %v", got, err)
	}

	sessions.WithAccessSource(&fakeAccess{user: nil})
	if got, err := sessions.Load(ctx, token); got != nil || err != nil {
		t.Fatalf("revoked session loaded: %+v, %v", got, err)
	}
	sessions.WithAccessSource(nil)
	if got, _ := sessions.Get(ctx, token); got != nil {
		t.Error("revoked session must be removed from Redis")
	}
}
