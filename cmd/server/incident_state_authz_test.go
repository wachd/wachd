package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wachd/wachd/internal/auth"
)

// D12: acknowledging, snoozing, resolving and reopening change incident state
// and need at least the responder role. Viewers are read-only.
func TestIncidentStateChanges_RequireResponder(t *testing.T) {
	team := uuid.New()
	s := &Server{}
	handlers := map[string]http.HandlerFunc{
		"ack":     s.handleAcknowledgeIncident,
		"resolve": s.handleResolveIncident,
		"reopen":  s.handleReopenIncident,
		"snooze":  s.handleSnoozeIncident,
	}
	cases := []struct {
		role      string
		super     bool
		forbidden bool
	}{
		{"viewer", false, true},
		{"", false, true}, // no grant at all
		{"responder", false, false},
		{"admin", false, false},
		{"", true, false}, // superadmin
	}
	for name, h := range handlers {
		for _, c := range cases {
			sess := &auth.Session{IsSuperAdmin: c.super, Roles: map[string]string{}}
			if c.role != "" {
				sess.TeamIDs = []uuid.UUID{team}
				sess.Roles[team.String()] = c.role
			}
			// An invalid incident id lets allowed callers stop at 400 before any
			// database access, so only the authorization decision is observed.
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req = mux.SetURLVars(req, map[string]string{"teamId": team.String(), "incidentId": "not-a-uuid"})
			req = req.WithContext(auth.ContextWithSession(req.Context(), sess))
			rec := httptest.NewRecorder()
			h(rec, req)

			if c.forbidden && rec.Code != http.StatusForbidden {
				t.Errorf("%s as role=%q super=%v: status %d, want 403", name, c.role, c.super, rec.Code)
			}
			if !c.forbidden && rec.Code == http.StatusForbidden {
				t.Errorf("%s as role=%q super=%v: unexpectedly forbidden", name, c.role, c.super)
			}
		}
	}
}
