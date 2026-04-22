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
	"time"

	"github.com/google/uuid"
)

// ConfigStore is the interface for all team configuration data — everything
// that will eventually live in wachd.yaml when Config-as-Code mode is enabled.
//
// Today this is satisfied by *DB (PostgreSQL). Future: *YAMLConfigStore reads
// from a Git-tracked YAML file with zero changes to callers.
//
// Rule: incidents, audit log, and snooze state are NOT here — those are events
// and always live in PostgreSQL (*DB directly).
type ConfigStore interface {
	// Teams
	GetTeam(ctx context.Context, teamID uuid.UUID) (*Team, error)
	GetTeamByWebhookSecret(ctx context.Context, secret string) (*Team, error)

	// Team config (data sources, notification channels)
	GetTeamConfig(ctx context.Context, teamID uuid.UUID) (*TeamConfig, error)
	UpsertTeamConfig(ctx context.Context, tc *TeamConfig) error

	// Members
	GetTeamMembers(ctx context.Context, teamID uuid.UUID) ([]*TeamMember, error)
	GetMemberByID(ctx context.Context, id uuid.UUID) (*TeamMember, error)
	UpdateMemberPhone(ctx context.Context, id uuid.UUID, source string, phone *string) error

	// Schedules
	GetSchedule(ctx context.Context, teamID uuid.UUID) (*Schedule, error)
	GetScheduleByID(ctx context.Context, id, teamID uuid.UUID) (*Schedule, error)
	GetScheduleForAPI(ctx context.Context, teamID uuid.UUID) (*ScheduleResponse, error)
	ListSchedules(ctx context.Context, teamID uuid.UUID) ([]*Schedule, error)
	UpsertSchedule(ctx context.Context, s *Schedule) error

	// Schedule overrides
	GetActiveOverrideForSchedule(ctx context.Context, scheduleID, teamID uuid.UUID, t time.Time) (*ScheduleOverride, error)
	ListOverridesForSchedule(ctx context.Context, scheduleID, teamID uuid.UUID) ([]ScheduleOverride, error)
	ListOverridesForRange(ctx context.Context, scheduleID, teamID uuid.UUID, from, to time.Time) ([]ScheduleOverride, error)
	CreateOverride(ctx context.Context, o *ScheduleOverride) error
	DeleteOverride(ctx context.Context, overrideID, teamID uuid.UUID) error

	// Escalation policy
	GetEscalationPolicy(ctx context.Context, teamID uuid.UUID) (*EscalationPolicy, error)
	UpsertEscalationPolicy(ctx context.Context, p *EscalationPolicy) error
}
