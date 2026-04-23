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

package oncall

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

// ── mockConfigStore ───────────────────────────────────────────────────────────

// mockConfigStore implements store.ConfigStore for testing without a DB.
type mockConfigStore struct {
	schedule         *store.Schedule
	scheduleErr      error
	override         *store.ScheduleOverride
	overrideErr      error
	member           *store.TeamMember
	memberErr        error
	escalationPolicy *store.EscalationPolicy
}

func (m *mockConfigStore) GetSchedule(_ context.Context, _ uuid.UUID) (*store.Schedule, error) {
	return m.schedule, m.scheduleErr
}
func (m *mockConfigStore) GetScheduleByID(_ context.Context, id, _ uuid.UUID) (*store.Schedule, error) {
	if m.schedule != nil && m.schedule.ID == id {
		return m.schedule, nil
	}
	return nil, nil
}
func (m *mockConfigStore) GetActiveOverrideForSchedule(_ context.Context, _, _ uuid.UUID, _ time.Time) (*store.ScheduleOverride, error) {
	return m.override, m.overrideErr
}
func (m *mockConfigStore) GetMemberByID(_ context.Context, _ uuid.UUID) (*store.TeamMember, error) {
	return m.member, m.memberErr
}
func (m *mockConfigStore) GetEscalationPolicy(_ context.Context, _ uuid.UUID) (*store.EscalationPolicy, error) {
	return m.escalationPolicy, nil
}

// Unused interface methods — return zero values.
func (m *mockConfigStore) GetTeam(_ context.Context, _ uuid.UUID) (*store.Team, error) {
	return nil, nil
}
func (m *mockConfigStore) GetTeamByWebhookSecret(_ context.Context, _ string) (*store.Team, error) {
	return nil, nil
}
func (m *mockConfigStore) GetTeamConfig(_ context.Context, _ uuid.UUID) (*store.TeamConfig, error) {
	return nil, nil
}
func (m *mockConfigStore) UpsertTeamConfig(_ context.Context, _ *store.TeamConfig) error {
	return nil
}
func (m *mockConfigStore) GetTeamMembers(_ context.Context, _ uuid.UUID) ([]*store.TeamMember, error) {
	return nil, nil
}
func (m *mockConfigStore) UpdateMemberPhone(_ context.Context, _ uuid.UUID, _ string, _ *string) error {
	return nil
}
func (m *mockConfigStore) GetScheduleForAPI(_ context.Context, _ uuid.UUID) (*store.ScheduleResponse, error) {
	return nil, nil
}
func (m *mockConfigStore) UpsertSchedule(_ context.Context, _ *store.Schedule) error { return nil }
func (m *mockConfigStore) ListSchedules(_ context.Context, _ uuid.UUID) ([]*store.Schedule, error) {
	return nil, nil
}
func (m *mockConfigStore) ListOverridesForSchedule(_ context.Context, _, _ uuid.UUID) ([]store.ScheduleOverride, error) {
	return nil, nil
}
func (m *mockConfigStore) ListOverridesForRange(_ context.Context, _, _ uuid.UUID, _, _ time.Time) ([]store.ScheduleOverride, error) {
	return nil, nil
}
func (m *mockConfigStore) CreateOverride(_ context.Context, _ *store.ScheduleOverride) error {
	return nil
}
func (m *mockConfigStore) DeleteOverride(_ context.Context, _, _ uuid.UUID) error { return nil }
func (m *mockConfigStore) UpsertEscalationPolicy(_ context.Context, _ *store.EscalationPolicy) error {
	return nil
}
func (m *mockConfigStore) ListUserNotificationRules(_ context.Context, _ uuid.UUID, _ string) ([]*store.UserNotificationRule, error) {
	return nil, nil
}
func (m *mockConfigStore) GetUserNotificationRules(_ context.Context, _ uuid.UUID, _ string, _ string) ([]*store.UserNotificationRule, error) {
	return nil, nil
}
func (m *mockConfigStore) UpsertUserNotificationRule(_ context.Context, _ *store.UserNotificationRule) (*store.UserNotificationRule, error) {
	return nil, nil
}
func (m *mockConfigStore) UpdateUserNotificationRule(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ bool, _ int) (*store.UserNotificationRule, error) {
	return nil, nil
}
func (m *mockConfigStore) DeleteUserNotificationRule(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockConfigStore) QueuePendingNotification(_ context.Context, _ *store.PendingNotification) error {
	return nil
}
func (m *mockConfigStore) GetDuePendingNotifications(_ context.Context) ([]*store.PendingNotification, error) {
	return nil, nil
}
func (m *mockConfigStore) MarkPendingNotificationSent(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *mockConfigStore) CancelPendingNotificationsForIncident(_ context.Context, _ uuid.UUID) error {
	return nil
}

// ── resolveWeekly ─────────────────────────────────────────────────────────────

func TestResolveWeekly_MatchesDay(t *testing.T) {
	userID := uuid.New()
	// Use a known Monday
	monday := time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)

	rot := WeeklyRotation{
		Type: "weekly",
		Rotation: []DayAssignment{
			{Day: "monday", UserID: userID},
			{Day: "tuesday", UserID: uuid.New()},
		},
	}
	raw, _ := json.Marshal(rot)

	uid, err := resolveWeekly(raw, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userID {
		t.Errorf("expected userID for monday, got %v", uid)
	}
}

func TestResolveWeekly_NoMatch(t *testing.T) {
	// Sunday not in rotation
	sunday := time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)
	rot := WeeklyRotation{
		Type: "weekly",
		Rotation: []DayAssignment{
			{Day: "monday", UserID: uuid.New()},
		},
	}
	raw, _ := json.Marshal(rot)

	uid, err := resolveWeekly(raw, sunday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil for unmatched day, got %v", uid)
	}
}

func TestResolveWeekly_InvalidJSON(t *testing.T) {
	_, err := resolveWeekly([]byte("not-json"), time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestResolveWeekly_AllDays(t *testing.T) {
	users := make([]uuid.UUID, 7)
	days := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	for i := range users {
		users[i] = uuid.New()
	}

	rot := WeeklyRotation{Type: "weekly"}
	for i, day := range days {
		rot.Rotation = append(rot.Rotation, DayAssignment{Day: day, UserID: users[i]})
	}
	raw, _ := json.Marshal(rot)

	// Jan 5-11 2025: Sun=5, Mon=6, ..., Sat=11
	base := time.Date(2025, 1, 5, 10, 0, 0, 0, time.UTC)
	for i, day := range days {
		at := base.Add(time.Duration(i) * 24 * time.Hour)
		uid, err := resolveWeekly(raw, at)
		if err != nil {
			t.Fatalf("error for %s: %v", day, err)
		}
		if uid != users[i] {
			t.Errorf("wrong user for %s: got %v, want %v", day, uid, users[i])
		}
	}
}

// ── resolveLayered ────────────────────────────────────────────────────────────

func TestResolveLayered_Always(t *testing.T) {
	userA := uuid.New()
	userB := uuid.New()
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC) // Monday

	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 168, // weekly
		Layers: []Layer{
			{
				ID:              "layer-1",
				Name:            "Primary",
				LayerOrder:      1,
				TimeRestriction: TimeRestriction{Type: "always"},
				Members:         []uuid.UUID{userA, userB},
			},
		},
	}
	raw, _ := json.Marshal(rot)

	// Week 0 → index 0 → userA
	uid, err := resolveLayered(raw, anchor.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userA {
		t.Errorf("expected userA in week 0, got %v", uid)
	}

	// Week 1 → index 1 → userB
	uid, err = resolveLayered(raw, anchor.Add(168*time.Hour+time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userB {
		t.Errorf("expected userB in week 1, got %v", uid)
	}

	// Week 2 → index 0 → userA (wraps)
	uid, err = resolveLayered(raw, anchor.Add(336*time.Hour+time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userA {
		t.Errorf("expected userA in week 2 (wrap), got %v", uid)
	}
}

func TestResolveLayered_WeekdaysOnly(t *testing.T) {
	userID := uuid.New()
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 168,
		Layers: []Layer{
			{
				ID:              "layer-1",
				Name:            "Weekdays",
				LayerOrder:      1,
				TimeRestriction: TimeRestriction{Type: "weekdays"},
				Members:         []uuid.UUID{userID},
			},
		},
	}
	raw, _ := json.Marshal(rot)

	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	uid, err := resolveLayered(raw, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userID {
		t.Errorf("expected user on weekday, got %v", uid)
	}

	saturday := time.Date(2025, 1, 11, 10, 0, 0, 0, time.UTC)
	uid, err = resolveLayered(raw, saturday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil on weekend, got %v", uid)
	}
}

func TestResolveLayered_WeekendsOnly(t *testing.T) {
	userID := uuid.New()
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 168,
		Layers: []Layer{
			{
				ID:              "layer-weekend",
				Name:            "Weekend",
				LayerOrder:      1,
				TimeRestriction: TimeRestriction{Type: "weekends"},
				Members:         []uuid.UUID{userID},
			},
		},
	}
	raw, _ := json.Marshal(rot)

	saturday := time.Date(2025, 1, 11, 10, 0, 0, 0, time.UTC)
	uid, err := resolveLayered(raw, saturday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userID {
		t.Errorf("expected user on saturday, got %v", uid)
	}

	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	uid, err = resolveLayered(raw, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil on weekday, got %v", uid)
	}
}

func TestResolveLayered_NoLayers(t *testing.T) {
	rot := LayeredRotation{Type: "layered", RotationIntervalHours: 168}
	raw, _ := json.Marshal(rot)
	_, err := resolveLayered(raw, time.Now())
	if err == nil {
		t.Error("expected error for layered rotation with no layers")
	}
}

func TestResolveLayered_NoMembers(t *testing.T) {
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 168,
		Layers: []Layer{
			{ID: "l1", Name: "Empty", LayerOrder: 1,
				TimeRestriction: TimeRestriction{Type: "always"},
				Members:         []uuid.UUID{}},
		},
	}
	raw, _ := json.Marshal(rot)

	uid, err := resolveLayered(raw, anchor.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != uuid.Nil {
		t.Errorf("expected uuid.Nil for empty members layer, got %v", uid)
	}
}

func TestResolveLayered_InvalidAnchor_FallsBackToStartOfWeek(t *testing.T) {
	userID := uuid.New()
	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         "not-a-valid-time",
		RotationIntervalHours: 168,
		Layers: []Layer{
			{ID: "l1", Name: "Primary", LayerOrder: 1,
				TimeRestriction: TimeRestriction{Type: "always"},
				Members:         []uuid.UUID{userID}},
		},
	}
	raw, _ := json.Marshal(rot)

	// Should not error — falls back to startOfWeek
	uid, err := resolveLayered(raw, time.Now())
	if err != nil {
		t.Fatalf("unexpected error with invalid anchor: %v", err)
	}
	if uid == uuid.Nil {
		t.Error("expected a user even with invalid anchor")
	}
}

func TestResolveLayered_DefaultInterval(t *testing.T) {
	userID := uuid.New()
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 0, // should default to 168
		Layers: []Layer{
			{ID: "l1", Name: "Primary", LayerOrder: 1,
				TimeRestriction: TimeRestriction{Type: "always"},
				Members:         []uuid.UUID{userID}},
		},
	}
	raw, _ := json.Marshal(rot)

	uid, err := resolveLayered(raw, anchor.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != userID {
		t.Errorf("expected userID with default interval, got %v", uid)
	}
}

// ── ResolveAllLayersAt ────────────────────────────────────────────────────────

func TestResolveAllLayersAt_Weekly(t *testing.T) {
	userID := uuid.New()
	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: "monday", UserID: userID}},
	}
	raw, _ := json.Marshal(rot)

	results, err := ResolveAllLayersAt(raw, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].UserID != userID {
		t.Errorf("expected userID, got %v", results[0].UserID)
	}
	if results[0].LayerName != "On-Call" {
		t.Errorf("expected 'On-Call' layer name, got %q", results[0].LayerName)
	}
}

func TestResolveAllLayersAt_Layered_MultipleLayers(t *testing.T) {
	primaryUser := uuid.New()
	secondaryUser := uuid.New()
	anchor := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)

	rot := LayeredRotation{
		Type:                  "layered",
		RotationStart:         anchor.Format(time.RFC3339),
		RotationIntervalHours: 168,
		Layers: []Layer{
			{ID: "primary", Name: "Primary", LayerOrder: 1,
				TimeRestriction: TimeRestriction{Type: "always"},
				Members:         []uuid.UUID{primaryUser}},
			{ID: "secondary", Name: "Secondary", LayerOrder: 2,
				TimeRestriction: TimeRestriction{Type: "weekdays"},
				Members:         []uuid.UUID{secondaryUser}},
		},
	}
	raw, _ := json.Marshal(rot)

	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	results, err := ResolveAllLayersAt(raw, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].UserID != primaryUser {
		t.Errorf("primary layer: expected %v, got %v", primaryUser, results[0].UserID)
	}
	if results[1].UserID != secondaryUser {
		t.Errorf("secondary layer: expected %v, got %v", secondaryUser, results[1].UserID)
	}
}

func TestResolveAllLayersAt_InvalidJSON(t *testing.T) {
	_, err := ResolveAllLayersAt([]byte("{invalid"), time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── layerActiveAt ─────────────────────────────────────────────────────────────

func TestLayerActiveAt(t *testing.T) {
	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)    // weekday
	saturday := time.Date(2025, 1, 11, 10, 0, 0, 0, time.UTC) // weekend

	tests := []struct {
		tr     TimeRestriction
		t      time.Time
		active bool
	}{
		{TimeRestriction{Type: "always"}, monday, true},
		{TimeRestriction{Type: "always"}, saturday, true},
		{TimeRestriction{Type: ""}, monday, true}, // empty = always
		{TimeRestriction{Type: "weekdays"}, monday, true},
		{TimeRestriction{Type: "weekdays"}, saturday, false},
		{TimeRestriction{Type: "weekends"}, saturday, true},
		{TimeRestriction{Type: "weekends"}, monday, false},
		{TimeRestriction{Type: "unknown"}, monday, true}, // unknown = always
	}

	for _, tt := range tests {
		result := layerActiveAt(tt.tr, tt.t)
		if result != tt.active {
			t.Errorf("layerActiveAt(%q, %v) = %v, want %v",
				tt.tr.Type, tt.t.Weekday(), result, tt.active)
		}
	}
}

func TestLayerActiveAt_CustomWindow(t *testing.T) {
	// Window: Monday 09:00 to Friday 17:00
	tr := TimeRestriction{
		Type: "custom",
		Windows: []TimeWindow{
			{StartDay: "monday", StartTime: "09:00", EndDay: "friday", EndTime: "17:00"},
		},
	}

	// Monday 10:00 — inside window
	if !layerActiveAt(tr, time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)) {
		t.Error("expected active on Monday 10:00")
	}
	// Saturday 10:00 — outside window
	if layerActiveAt(tr, time.Date(2025, 1, 11, 10, 0, 0, 0, time.UTC)) {
		t.Error("expected inactive on Saturday")
	}
	// Friday 18:00 — after end time
	if layerActiveAt(tr, time.Date(2025, 1, 10, 18, 0, 0, 0, time.UTC)) {
		t.Error("expected inactive on Friday after 17:00")
	}
}

func TestLayerActiveAt_CustomWindow_Empty(t *testing.T) {
	tr := TimeRestriction{Type: "custom", Windows: nil}
	if layerActiveAt(tr, time.Now()) {
		t.Error("expected inactive for custom with no windows")
	}
}

// ── windowCovers ──────────────────────────────────────────────────────────────

func TestWindowCovers_Simple(t *testing.T) {
	w := TimeWindow{StartDay: "monday", StartTime: "09:00", EndDay: "friday", EndTime: "17:00"}

	tests := []struct {
		t      time.Time
		covers bool
	}{
		{time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC), true},   // Mon 09:00 exactly (inclusive)
		{time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC), true},  // Mon 12:00
		{time.Date(2025, 1, 10, 16, 59, 0, 0, time.UTC), true}, // Fri 16:59
		{time.Date(2025, 1, 10, 17, 0, 0, 0, time.UTC), false}, // Fri 17:00 (exclusive)
		{time.Date(2025, 1, 11, 10, 0, 0, 0, time.UTC), false}, // Sat
	}

	for _, tt := range tests {
		result := windowCovers(w, tt.t)
		if result != tt.covers {
			t.Errorf("windowCovers at %v = %v, want %v", tt.t, result, tt.covers)
		}
	}
}

func TestWindowCovers_WrapAroundMidnight(t *testing.T) {
	// Night shift: Friday 20:00 to Monday 08:00
	w := TimeWindow{StartDay: "friday", StartTime: "20:00", EndDay: "monday", EndTime: "08:00"}

	tests := []struct {
		t      time.Time
		covers bool
	}{
		{time.Date(2025, 1, 10, 22, 0, 0, 0, time.UTC), true},  // Fri 22:00
		{time.Date(2025, 1, 11, 12, 0, 0, 0, time.UTC), true},  // Sat noon
		{time.Date(2025, 1, 12, 3, 0, 0, 0, time.UTC), true},   // Sun 03:00
		{time.Date(2025, 1, 6, 7, 0, 0, 0, time.UTC), true},    // Mon 07:00
		{time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC), false},   // Mon 09:00 — outside
		{time.Date(2025, 1, 8, 12, 0, 0, 0, time.UTC), false},  // Wed noon — outside
	}

	for _, tt := range tests {
		result := windowCovers(w, tt.t)
		if result != tt.covers {
			t.Errorf("windowCovers(nightShift) at %v = %v, want %v", tt.t, result, tt.covers)
		}
	}
}

// ── Helper functions ──────────────────────────────────────────────────────────

func TestWeekdayName(t *testing.T) {
	tests := []struct {
		d    time.Weekday
		name string
	}{
		{time.Sunday, "sunday"},
		{time.Monday, "monday"},
		{time.Tuesday, "tuesday"},
		{time.Wednesday, "wednesday"},
		{time.Thursday, "thursday"},
		{time.Friday, "friday"},
		{time.Saturday, "saturday"},
	}
	for _, tt := range tests {
		if got := weekdayName(tt.d); got != tt.name {
			t.Errorf("weekdayName(%v) = %q, want %q", tt.d, got, tt.name)
		}
	}
}

func TestWeekdayIndex(t *testing.T) {
	tests := []struct {
		name  string
		index int
	}{
		{"monday", 0},
		{"tuesday", 1},
		{"wednesday", 2},
		{"thursday", 3},
		{"friday", 4},
		{"saturday", 5},
		{"sunday", 6},
		{"unknown", 0}, // unknown defaults to 0
	}
	for _, tt := range tests {
		if got := weekdayIndex(tt.name); got != tt.index {
			t.Errorf("weekdayIndex(%q) = %d, want %d", tt.name, got, tt.index)
		}
	}
}

func TestParseHHMM(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"00:00", 0},
		{"09:00", 540},
		{"12:30", 750},
		{"17:00", 1020},
		{"23:59", 1439},
		{"", 0},
		{"ab", 0},
	}
	for _, tt := range tests {
		if got := parseHHMM(tt.input); got != tt.expected {
			t.Errorf("parseHHMM(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestStartOfWeek(t *testing.T) {
	// Wednesday Jan 8 2025 → Monday Jan 6 2025
	wednesday := time.Date(2025, 1, 8, 15, 30, 0, 0, time.UTC)
	sow := startOfWeek(wednesday)

	if sow.Weekday() != time.Monday {
		t.Errorf("startOfWeek should return Monday, got %v", sow.Weekday())
	}
	if sow.Year() != 2025 || sow.Month() != 1 || sow.Day() != 6 {
		t.Errorf("startOfWeek = %v, want 2025-01-06", sow)
	}
	if sow.Hour() != 0 || sow.Minute() != 0 || sow.Second() != 0 {
		t.Error("startOfWeek should be at midnight")
	}
}

func TestStartOfWeek_Monday(t *testing.T) {
	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	sow := startOfWeek(monday)
	if sow.Day() != 6 {
		t.Errorf("startOfWeek of Monday should be same day, got %v", sow)
	}
}

// ── Manager (with mock) ───────────────────────────────────────────────────────

func TestManager_GetOnCallAt_NoSchedule(t *testing.T) {
	m := NewManager(&mockConfigStore{schedule: nil})
	member, err := m.GetOnCallAt(context.Background(), uuid.New(), time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if member != nil {
		t.Error("expected nil member when no schedule configured")
	}
}

func TestManager_GetOnCallAt_WithOverride(t *testing.T) {
	schedID := uuid.New()
	overrideUserID := uuid.New()
	teamID := uuid.New()
	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: "monday", UserID: uuid.New()}},
	}
	raw, _ := json.Marshal(rot)

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             schedID,
			TeamID:         teamID,
			RotationConfig: raw,
			Enabled:        true,
		},
		override: &store.ScheduleOverride{
			ID:     uuid.New(),
			UserID: overrideUserID,
		},
		member: &store.TeamMember{
			ID:   overrideUserID,
			Name: "Override User",
		},
	}

	m := NewManager(mock)
	member, err := m.GetOnCallAt(context.Background(), teamID, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if member == nil {
		t.Fatal("expected a member")
	}
	if member.ID != overrideUserID {
		t.Errorf("expected override user, got %v", member.ID)
	}
}

func TestManager_GetOnCallAt_WeeklyRotation(t *testing.T) {
	userID := uuid.New()
	teamID := uuid.New()
	monday := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: "monday", UserID: userID}},
	}
	raw, _ := json.Marshal(rot)

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             uuid.New(),
			TeamID:         teamID,
			RotationConfig: raw,
		},
		override: nil,
		member:   &store.TeamMember{ID: userID, Name: "Alice"},
	}

	m := NewManager(mock)
	member, err := m.GetOnCallAt(context.Background(), teamID, monday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if member == nil {
		t.Fatal("expected a member")
	}
	if member.ID != userID {
		t.Errorf("expected userID, got %v", member.ID)
	}
}

func TestManager_GetOnCallAt_NoCoverage(t *testing.T) {
	// Sunday not in rotation
	sunday := time.Date(2025, 1, 5, 10, 0, 0, 0, time.UTC)

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: "monday", UserID: uuid.New()}},
	}
	raw, _ := json.Marshal(rot)

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             uuid.New(),
			RotationConfig: raw,
		},
	}

	m := NewManager(mock)
	member, err := m.GetOnCallAt(context.Background(), uuid.New(), sunday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if member != nil {
		t.Error("expected nil member for uncovered day")
	}
}

// ── GetEscalationChain — with policy ─────────────────────────────────────────

func TestManager_GetEscalationChain_WithPolicy_TwoLayers(t *testing.T) {
	userID := uuid.New()
	schedID := uuid.New()
	teamID := uuid.New()

	// Rotation for today — ensures the layer resolves a user
	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: weekdayName(time.Now().Weekday()), UserID: userID}},
	}
	raw, _ := json.Marshal(rot)

	policyCfg, _ := json.Marshal(EscalationConfig{
		Layers: []EscalationLayer{
			{ScheduleID: schedID.String(), NotifyAfterMinutes: 0, LayerName: "primary"},
			{ScheduleID: schedID.String(), NotifyAfterMinutes: 10, LayerName: "secondary"},
		},
	})

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             schedID,
			TeamID:         teamID,
			RotationConfig: raw,
		},
		escalationPolicy: &store.EscalationPolicy{
			TeamID: teamID,
			Config: policyCfg,
		},
		member: &store.TeamMember{ID: userID, Name: "Alice"},
	}

	m := NewManager(mock)
	steps, err := m.GetEscalationChain(context.Background(), teamID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].LayerName != "primary" {
		t.Errorf("step 0: expected 'primary', got %q", steps[0].LayerName)
	}
	if steps[0].NotifyAfterMinutes != 0 {
		t.Errorf("step 0: expected 0 min, got %d", steps[0].NotifyAfterMinutes)
	}
	if steps[1].LayerName != "secondary" {
		t.Errorf("step 1: expected 'secondary', got %q", steps[1].LayerName)
	}
	if steps[1].NotifyAfterMinutes != 10 {
		t.Errorf("step 1: expected 10 min, got %d", steps[1].NotifyAfterMinutes)
	}
}

func TestManager_GetEscalationChain_WithOverride(t *testing.T) {
	regularUserID := uuid.New()
	overrideUserID := uuid.New()
	schedID := uuid.New()
	teamID := uuid.New()

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: weekdayName(time.Now().Weekday()), UserID: regularUserID}},
	}
	raw, _ := json.Marshal(rot)

	policyCfg, _ := json.Marshal(EscalationConfig{
		Layers: []EscalationLayer{
			{ScheduleID: schedID.String(), NotifyAfterMinutes: 0, LayerName: "primary"},
		},
	})

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             schedID,
			TeamID:         teamID,
			RotationConfig: raw,
		},
		escalationPolicy: &store.EscalationPolicy{
			TeamID: teamID,
			Config: policyCfg,
		},
		override: &store.ScheduleOverride{
			ID:     uuid.New(),
			UserID: overrideUserID,
		},
		member: &store.TeamMember{ID: overrideUserID, Name: "Override User"},
	}

	m := NewManager(mock)
	steps, err := m.GetEscalationChain(context.Background(), teamID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].User == nil || steps[0].User.ID != overrideUserID {
		t.Errorf("expected override user %v, got %v", overrideUserID, steps[0].User)
	}
}

func TestManager_GetEscalationChain_InvalidScheduleID_Skipped(t *testing.T) {
	teamID := uuid.New()

	policyCfg, _ := json.Marshal(EscalationConfig{
		Layers: []EscalationLayer{
			{ScheduleID: "not-a-valid-uuid", NotifyAfterMinutes: 0, LayerName: "bad"},
		},
	})

	mock := &mockConfigStore{
		escalationPolicy: &store.EscalationPolicy{
			TeamID: teamID,
			Config: policyCfg,
		},
	}

	m := NewManager(mock)
	_, err := m.GetEscalationChain(context.Background(), teamID)
	// Invalid UUID skipped → no resolvable layers → error
	if err == nil {
		t.Error("expected error when all schedule IDs are invalid UUIDs")
	}
}

func TestManager_GetEscalationChain_ScheduleNotFound_Skipped(t *testing.T) {
	teamID := uuid.New()

	// Policy references a schedule UUID that doesn't match the mock's schedule
	policyCfg, _ := json.Marshal(EscalationConfig{
		Layers: []EscalationLayer{
			{ScheduleID: uuid.New().String(), NotifyAfterMinutes: 0, LayerName: "missing"},
		},
	})

	mock := &mockConfigStore{
		schedule: nil, // no schedule in store
		escalationPolicy: &store.EscalationPolicy{
			TeamID: teamID,
			Config: policyCfg,
		},
	}

	m := NewManager(mock)
	_, err := m.GetEscalationChain(context.Background(), teamID)
	if err == nil {
		t.Error("expected error when schedule not found for all layers")
	}
}

func TestManager_GetEscalationChain_InvalidPolicyJSON(t *testing.T) {
	teamID := uuid.New()

	mock := &mockConfigStore{
		escalationPolicy: &store.EscalationPolicy{
			TeamID: teamID,
			Config: []byte(`{invalid json}`),
		},
	}

	m := NewManager(mock)
	_, err := m.GetEscalationChain(context.Background(), teamID)
	if err == nil {
		t.Error("expected error for invalid escalation policy JSON")
	}
}

func TestManager_GetEscalationChain_NoPolicy(t *testing.T) {
	userID := uuid.New()
	teamID := uuid.New()

	rot := WeeklyRotation{
		Type:     "weekly",
		Rotation: []DayAssignment{{Day: weekdayName(time.Now().Weekday()), UserID: userID}},
	}
	raw, _ := json.Marshal(rot)

	mock := &mockConfigStore{
		schedule: &store.Schedule{
			ID:             uuid.New(),
			TeamID:         teamID,
			RotationConfig: raw,
		},
		escalationPolicy: nil,
		member:           &store.TeamMember{ID: userID, Name: "Alice"},
	}

	m := NewManager(mock)
	steps, err := m.GetEscalationChain(context.Background(), teamID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step (fallback), got %d", len(steps))
	}
	if steps[0].NotifyAfterMinutes != 0 {
		t.Errorf("expected immediate notification, got %d min", steps[0].NotifyAfterMinutes)
	}
}
