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
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/store"
)

// ──────────────────────────────────────────────────────────────────────────────
// Rotation config types (stored as JSONB in schedules.rotation_config)
// ──────────────────────────────────────────────────────────────────────────────

// WeeklyRotation is the legacy format: each day maps to a specific user.
// Kept for backwards compatibility — new schedules should use LayeredRotation.
type WeeklyRotation struct {
	Type     string          `json:"type"` // "weekly"
	Rotation []DayAssignment `json:"rotation"`
}

// DayAssignment maps a weekday name to a user.
type DayAssignment struct {
	Day    string    `json:"day"`     // monday…sunday
	UserID uuid.UUID `json:"user_id"`
}

// LayeredRotation is the new format supporting multiple simultaneous on-call
// layers, time-window restrictions, and index-based rotation.
type LayeredRotation struct {
	Type                  string  `json:"type"` // "layered"
	RotationStart         string  `json:"rotation_start"`          // RFC3339 anchor time
	RotationIntervalHours float64 `json:"rotation_interval_hours"` // e.g. 168 = weekly
	Layers                []Layer `json:"layers"`
}

// Layer is a single on-call tier within a LayeredRotation.
type Layer struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	LayerOrder      int             `json:"layer_order"` // 1 = first notified
	TimeRestriction TimeRestriction `json:"time_restriction"`
	Members         []uuid.UUID     `json:"members"` // ordered list of user IDs
}

// TimeRestriction controls when a layer is active.
type TimeRestriction struct {
	// Type: "always" | "weekdays" | "weekends" | "custom"
	Type    string         `json:"type"`
	Windows []TimeWindow   `json:"windows,omitempty"` // only for type="custom"
}

// TimeWindow is a recurring weekly time block (start inclusive, end exclusive).
type TimeWindow struct {
	StartDay  string `json:"start_day"`  // monday…sunday
	StartTime string `json:"start_time"` // "HH:MM" (24h)
	EndDay    string `json:"end_day"`
	EndTime   string `json:"end_time"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Escalation config types (stored as JSONB in escalation_policies.config)
// ──────────────────────────────────────────────────────────────────────────────

// EscalationConfig is the full escalation policy for a team.
type EscalationConfig struct {
	Layers                []EscalationLayer `json:"layers"`
	RepeatIntervalMinutes int               `json:"repeat_interval_minutes"` // 0 = no repeat
	MaxRepeats            int               `json:"max_repeats"`
}

// EscalationLayer is one step in the escalation chain.
type EscalationLayer struct {
	ScheduleID          string `json:"schedule_id"`           // UUID of the schedule
	NotifyAfterMinutes  int    `json:"notify_after_minutes"`  // 0 = notify immediately
	LayerName           string `json:"layer_name"`
}

// EscalationStep is a resolved escalation step with the actual member populated.
type EscalationStep struct {
	User               *store.TeamMember
	NotifyAfterMinutes int
	LayerName          string
}

// LayerResult is the resolved on-call user for a single rotation layer.
type LayerResult struct {
	LayerName string
	UserID    uuid.UUID // uuid.Nil if not covered at this time
}

// ResolveAllLayersAt returns coverage for ALL rotation layers at a given time.
// For the weekly (legacy) format, a single "On-Call" layer is returned.
// For the layered format, one result is returned per layer (whether active or not).
func ResolveAllLayersAt(raw []byte, t time.Time) ([]LayerResult, error) {
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, fmt.Errorf("invalid rotation config: %w", err)
	}
	switch peek.Type {
	case "layered":
		return resolveLayeredAllLayers(raw, t)
	default: // "weekly" and legacy
		uid, err := resolveWeekly(raw, t)
		if err != nil {
			return nil, err
		}
		return []LayerResult{{LayerName: "On-Call", UserID: uid}}, nil
	}
}

func resolveLayeredAllLayers(raw []byte, t time.Time) ([]LayerResult, error) {
	var rot LayeredRotation
	if err := json.Unmarshal(raw, &rot); err != nil {
		return nil, err
	}
	if rot.RotationIntervalHours <= 0 {
		rot.RotationIntervalHours = 168
	}
	anchor, err := time.Parse(time.RFC3339, rot.RotationStart)
	if err != nil {
		anchor = startOfWeek(t)
	}
	intervalDur := time.Duration(float64(time.Hour) * rot.RotationIntervalHours)
	elapsed := t.Sub(anchor)
	if elapsed < 0 {
		elapsed = 0
	}
	var results []LayerResult
	for _, layer := range rot.Layers {
		name := layer.Name
		if name == "" {
			name = fmt.Sprintf("Layer %d", layer.LayerOrder)
		}
		if len(layer.Members) == 0 || !layerActiveAt(layer.TimeRestriction, t) {
			results = append(results, LayerResult{LayerName: name, UserID: uuid.Nil})
			continue
		}
		idx := int(elapsed/intervalDur) % len(layer.Members)
		results = append(results, LayerResult{LayerName: name, UserID: layer.Members[idx]})
	}
	return results, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Manager
// ──────────────────────────────────────────────────────────────────────────────

// Manager handles on-call scheduling.
type Manager struct {
	cfg store.ConfigStore
}

// NewManager creates a new on-call manager.
func NewManager(cfg store.ConfigStore) *Manager {
	return &Manager{cfg: cfg}
}

// GetCurrentOnCall returns the member who is currently on-call for a team.
// It checks for active overrides first, then falls back to the rotation.
func (m *Manager) GetCurrentOnCall(ctx context.Context, teamID uuid.UUID) (*store.TeamMember, error) {
	return m.GetOnCallAt(ctx, teamID, time.Now())
}

// GetOnCallAt returns the on-call member at a specific time, respecting overrides.
func (m *Manager) GetOnCallAt(ctx context.Context, teamID uuid.UUID, t time.Time) (*store.TeamMember, error) {
	schedule, err := m.cfg.GetSchedule(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("loading schedule: %w", err)
	}
	if schedule == nil {
		return nil, nil // no schedule configured — caller decides how to handle
	}

	// Check for an active override first.
	override, err := m.cfg.GetActiveOverrideForSchedule(ctx, schedule.ID, teamID, t)
	if err != nil {
		return nil, fmt.Errorf("checking overrides: %w", err)
	}
	if override != nil {
		member, err := m.cfg.GetMemberByID(ctx, override.UserID)
		if err != nil {
			return nil, fmt.Errorf("loading override member: %w", err)
		}
		return member, nil
	}

	// No override — resolve from the rotation.
	return m.resolveRotation(ctx, schedule, t)
}

// GetEscalationChain returns the ordered list of users to notify for a team,
// based on the team's escalation policy. Each step carries its notify delay.
// Falls back to a single-step chain using the schedule's current on-call user
// if no escalation policy is configured.
func (m *Manager) GetEscalationChain(ctx context.Context, teamID uuid.UUID) ([]EscalationStep, error) {
	policy, err := m.cfg.GetEscalationPolicy(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("loading escalation policy: %w", err)
	}

	if policy == nil {
		// No policy: single-step using the primary schedule.
		user, err := m.GetCurrentOnCall(ctx, teamID)
		if err != nil {
			return nil, err
		}
		return []EscalationStep{{User: user, NotifyAfterMinutes: 0, LayerName: "primary"}}, nil
	}

	var cfg EscalationConfig
	if err := json.Unmarshal(policy.Config, &cfg); err != nil {
		return nil, fmt.Errorf("parsing escalation config: %w", err)
	}

	now := time.Now()
	var steps []EscalationStep
	for i, layer := range cfg.Layers {
		schedID, err := uuid.Parse(layer.ScheduleID)
		if err != nil {
			log.Printf("escalation layer %d: invalid schedule_id %q: %v", i, layer.ScheduleID, err)
			continue
		}
		schedule, err := m.cfg.GetScheduleByID(ctx, schedID, teamID)
		if err != nil {
			log.Printf("escalation layer %d (%s): schedule not found: %v", i, layer.LayerName, err)
			continue
		}
		if schedule == nil {
			log.Printf("escalation layer %d (%s): schedule %s not found", i, layer.LayerName, schedID)
			continue
		}
		override, err := m.cfg.GetActiveOverrideForSchedule(ctx, schedule.ID, teamID, now)
		if err != nil {
			log.Printf("escalation layer %d (%s): override check failed: %v", i, layer.LayerName, err)
			// Fall through to rotation — don't skip the layer entirely.
		}
		var memberID uuid.UUID
		if override != nil {
			memberID = override.UserID
		} else {
			uid, err := m.resolveRotationUserID(schedule, now)
			if err != nil {
				log.Printf("escalation layer %d (%s): rotation resolve failed: %v", i, layer.LayerName, err)
				continue
			}
			if uid == uuid.Nil {
				log.Printf("escalation layer %d (%s): no coverage at current time, skipping", i, layer.LayerName)
				continue
			}
			memberID = uid
		}
		member, err := m.cfg.GetMemberByID(ctx, memberID)
		if err != nil {
			log.Printf("escalation layer %d (%s): member %s not found: %v", i, layer.LayerName, memberID, err)
			continue
		}
		steps = append(steps, EscalationStep{
			User:               member,
			NotifyAfterMinutes: layer.NotifyAfterMinutes,
			LayerName:          layer.LayerName,
		})
	}

	if len(steps) == 0 {
		return nil, fmt.Errorf("escalation policy has no resolvable layers for team %s", teamID)
	}
	return steps, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────────────────

func (m *Manager) resolveRotation(ctx context.Context, schedule *store.Schedule, t time.Time) (*store.TeamMember, error) {
	memberID, err := m.resolveRotationUserID(schedule, t)
	if err != nil {
		return nil, err
	}
	if memberID == uuid.Nil {
		return nil, nil // today is not covered by the rotation
	}
	member, err := m.cfg.GetMemberByID(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("loading on-call member: %w", err)
	}
	return member, nil
}

func (m *Manager) resolveRotationUserID(schedule *store.Schedule, t time.Time) (uuid.UUID, error) {
	// Peek at the type field to decide which format to parse.
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(schedule.RotationConfig, &peek); err != nil {
		return uuid.Nil, fmt.Errorf("invalid rotation config: %w", err)
	}

	switch peek.Type {
	case "layered":
		return resolveLayered(schedule.RotationConfig, t)
	default: // "weekly" and legacy formats
		return resolveWeekly(schedule.RotationConfig, t)
	}
}

// resolveWeekly handles the legacy day-indexed format.
func resolveWeekly(raw []byte, t time.Time) (uuid.UUID, error) {
	var rot WeeklyRotation
	if err := json.Unmarshal(raw, &rot); err != nil {
		return uuid.Nil, err
	}
	dayName := weekdayName(t.Weekday())
	for _, a := range rot.Rotation {
		if a.Day == dayName {
			return a.UserID, nil
		}
	}
	return uuid.Nil, nil // today not covered — not an error
}

// resolveLayered handles the new LayeredRotation format.
// It uses the first active layer (lowest LayerOrder) whose time restriction covers t.
func resolveLayered(raw []byte, t time.Time) (uuid.UUID, error) {
	var rot LayeredRotation
	if err := json.Unmarshal(raw, &rot); err != nil {
		return uuid.Nil, err
	}
	if len(rot.Layers) == 0 {
		return uuid.Nil, fmt.Errorf("layered rotation has no layers")
	}
	if rot.RotationIntervalHours <= 0 {
		rot.RotationIntervalHours = 168 // default: weekly
	}

	anchor, err := time.Parse(time.RFC3339, rot.RotationStart)
	if err != nil {
		// Fallback: use Monday of the current week.
		anchor = startOfWeek(t)
	}

	// Sort layers by order (assume already sorted; pick first active).
	for _, layer := range rot.Layers {
		if len(layer.Members) == 0 {
			continue
		}
		if !layerActiveAt(layer.TimeRestriction, t) {
			continue
		}
		intervalDur := time.Duration(float64(time.Hour) * rot.RotationIntervalHours)
		elapsed := t.Sub(anchor)
		if elapsed < 0 {
			elapsed = 0
		}
		idx := int(elapsed/intervalDur) % len(layer.Members)
		return layer.Members[idx], nil
	}

	return uuid.Nil, nil // no active layer at this time — not an error
}

// layerActiveAt returns true if the time restriction allows the layer at time t.
func layerActiveAt(tr TimeRestriction, t time.Time) bool {
	switch tr.Type {
	case "always", "":
		return true
	case "weekdays":
		wd := t.Weekday()
		return wd >= time.Monday && wd <= time.Friday
	case "weekends":
		wd := t.Weekday()
		return wd == time.Saturday || wd == time.Sunday
	case "custom":
		for _, w := range tr.Windows {
			if windowCovers(w, t) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// windowCovers checks if a recurring TimeWindow covers time t.
// It handles windows that span midnight (end_day < start_day).
func windowCovers(w TimeWindow, t time.Time) bool {
	startDayIdx := weekdayIndex(w.StartDay)
	endDayIdx := weekdayIndex(w.EndDay)
	tDayIdx := int(t.Weekday()+6) % 7 // Monday=0

	startMins := parseHHMM(w.StartTime)
	endMins := parseHHMM(w.EndTime)
	tMins := t.Hour()*60 + t.Minute()

	startTotal := startDayIdx*1440 + startMins
	endTotal := endDayIdx*1440 + endMins
	tTotal := tDayIdx*1440 + tMins

	if startTotal <= endTotal {
		return tTotal >= startTotal && tTotal < endTotal
	}
	// Wraps around the week boundary.
	return tTotal >= startTotal || tTotal < endTotal
}

func weekdayName(d time.Weekday) string {
	return [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}[d]
}

func weekdayIndex(name string) int {
	m := map[string]int{
		"monday": 0, "tuesday": 1, "wednesday": 2, "thursday": 3,
		"friday": 4, "saturday": 5, "sunday": 6,
	}
	if i, ok := m[name]; ok {
		return i
	}
	return 0
}

func parseHHMM(s string) int {
	if len(s) < 5 {
		return 0
	}
	h, m := 0, 0
	_, _ = fmt.Sscanf(s, "%d:%d", &h, &m)
	return h*60 + m
}

func startOfWeek(t time.Time) time.Time {
	offset := int(t.Weekday()+6) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-offset, 0, 0, 0, 0, t.Location())
}
