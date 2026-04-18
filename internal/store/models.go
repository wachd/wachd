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
	"time"

	"github.com/google/uuid"
)

// Team represents a team in the system (multi-tenancy boundary)
type Team struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	WebhookSecret string    `json:"webhook_secret"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// User represents an on-call engineer
type User struct {
	ID        uuid.UUID `json:"id"`
	TeamID    uuid.UUID `json:"team_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Phone     *string   `json:"phone,omitempty"`
	Role      string    `json:"role"` // viewer, responder, admin, security_analyst
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Incident represents an alert/incident
type Incident struct {
	ID             uuid.UUID  `json:"id"`
	TeamID         uuid.UUID  `json:"team_id"`
	Title          string     `json:"title"`
	Message        *string    `json:"message,omitempty"`
	Severity       string     `json:"severity"` // critical, high, medium, low, unknown
	Status         string     `json:"status"`   // open, acknowledged, resolved, snoozed
	Source         string     `json:"source"`   // grafana, datadog, prometheus, etc.
	AlertPayload   []byte     `json:"alert_payload"`
	Context        []byte     `json:"context,omitempty"`
	Analysis       []byte     `json:"analysis,omitempty"`
	FiredAt        time.Time  `json:"fired_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	SnoozedUntil   *time.Time `json:"snoozed_until,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	AssignedTo     *uuid.UUID `json:"assigned_to,omitempty"`
}

// Schedule represents an on-call schedule
type Schedule struct {
	ID             uuid.UUID `json:"id"`
	TeamID         uuid.UUID `json:"team_id"`
	Name           string    `json:"name"`
	RotationConfig []byte    `json:"rotation_config"` // JSONB
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ScheduleOverride replaces the scheduled on-call user for a specific time window.
type ScheduleOverride struct {
	ID         uuid.UUID `json:"id"`
	ScheduleID uuid.UUID `json:"schedule_id"`
	TeamID     uuid.UUID `json:"team_id"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
	UserID     uuid.UUID `json:"user_id"`
	Reason     *string   `json:"reason,omitempty"`
	CreatedBy  uuid.UUID `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// EscalationPolicy defines the ordered notification chain for a team.
// Config is a JSON blob matching EscalationConfig in the oncall package.
type EscalationPolicy struct {
	ID        uuid.UUID `json:"id"`
	TeamID    uuid.UUID `json:"team_id"`
	Config    []byte    `json:"config"` // JSONB
	UpdatedAt time.Time `json:"updated_at"`
}

// SSOIdentity is a provider-level identity (one per person, not per team)
type SSOIdentity struct {
	ID         uuid.UUID  `json:"id"`
	Provider   string     `json:"provider"`    // entra | google | okta
	ProviderID string     `json:"provider_id"` // oid claim
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	AvatarURL  *string    `json:"avatar_url,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// TeamAccess links an SSO identity to a team with a role
type TeamAccess struct {
	IdentityID uuid.UUID `json:"identity_id"`
	TeamID     uuid.UUID `json:"team_id"`
	TeamName   string    `json:"team_name"`
	Role       string    `json:"role"` // viewer | responder | admin
}

// GroupMapping maps a provider group to a wachd team
type GroupMapping struct {
	ID            uuid.UUID  `json:"id"`
	Provider      string     `json:"provider"`
	SSOProviderID *uuid.UUID `json:"sso_provider_id,omitempty"`
	GroupID       string     `json:"group_id"`
	GroupName     *string    `json:"group_name,omitempty"`
	TeamID        uuid.UUID  `json:"team_id"`
	Role          string     `json:"role"`
	CreatedAt     time.Time  `json:"created_at"`
}

// TeamConfig represents team-specific configuration
type TeamConfig struct {
	TeamID                uuid.UUID  `json:"team_id"`
	SlackWebhookURL       *string    `json:"slack_webhook_url,omitempty"`
	SlackChannel          *string    `json:"slack_channel,omitempty"`
	SlackBotToken         *string    `json:"slack_bot_token,omitempty"`
	GitHubTokenEncrypted  *string    `json:"github_token_encrypted,omitempty"`
	GitHubRepos           []byte     `json:"github_repos,omitempty"` // JSONB
	PrometheusEndpoint          *string    `json:"prometheus_endpoint,omitempty"`
	LokiEndpoint                *string    `json:"loki_endpoint,omitempty"`
	DynatraceEndpoint           *string    `json:"dynatrace_endpoint,omitempty"`
	DynatraceTokenEncrypted     *string    `json:"dynatrace_token_encrypted,omitempty"`
	SplunkEndpoint              *string    `json:"splunk_endpoint,omitempty"`
	SplunkTokenEncrypted        *string    `json:"splunk_token_encrypted,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// SystemConfig holds platform-wide settings managed by the superadmin only.
// There is exactly one row in the system_config table (id = 1).
type SystemConfig struct {
	AIBackend string     `json:"ai_backend"` // ollama | claude | openai | gemini
	AIModel   *string    `json:"ai_model,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
	UpdatedBy *uuid.UUID `json:"updated_by,omitempty"`
}

// ── Enterprise Auth Models ────────────────────────────────────────────────────

// TeamMember is the unified on-call identity used throughout the notification
// and scheduling stack. It is resolved from local_users or sso_identities via
// team access tables — never from the legacy on-call contacts table.
type TeamMember struct {
	ID     uuid.UUID `json:"id"`
	Source string    `json:"source"` // "local" | "sso"
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
	Email  string    `json:"email"`
	Phone  *string   `json:"phone,omitempty"`
	Role   string    `json:"role"` // resolved from group access / sso_team_access
}

// LocalUser is a non-SSO auth identity (includes bootstrap admin).
type LocalUser struct {
	ID                   uuid.UUID  `json:"id"`
	Username             string     `json:"username"`
	Email                string     `json:"email"`
	Name                 string     `json:"name"`
	Phone                *string    `json:"phone,omitempty"`
	PasswordHash         string     `json:"-"` // never serialized
	IsSuperAdmin         bool       `json:"is_superadmin"`
	IsActive             bool       `json:"is_active"`
	ForcePasswordChange  bool       `json:"force_password_change"`
	FailedLoginAttempts  int        `json:"failed_login_attempts"`
	LockedUntil          *time.Time `json:"locked_until,omitempty"`
	LastLoginAt          *time.Time `json:"last_login_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// LocalUserUpdate holds the mutable fields for an admin user update.
type LocalUserUpdate struct {
	Email    *string
	Name     *string
	IsActive *bool
}

// LocalGroup is a superadmin-managed group of local users with team access.
type LocalGroup struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SSOProvider holds DB-stored OIDC provider configuration.
// ClientSecretEnc is never exposed via the API — callers see ClientSecretSet instead.
type SSOProvider struct {
	ID               uuid.UUID `json:"id"`
	Name             string    `json:"name"`
	ProviderType     string    `json:"provider_type"` // "oidc"
	IssuerURL        string    `json:"issuer_url"`
	ClientID         string    `json:"client_id"`
	ClientSecretEnc  string    `json:"-"` // AES-256-GCM encrypted, base64
	Scopes           []string  `json:"scopes"`
	Enabled          bool      `json:"enabled"`
	AutoProvision    bool      `json:"auto_provision"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SSOProviderPublic is the API-safe view of an SSOProvider.
type SSOProviderPublic struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	ProviderType    string    `json:"provider_type"`
	IssuerURL       string    `json:"issuer_url"`
	ClientID        string    `json:"client_id"`
	ClientSecretSet bool      `json:"client_secret_set"`
	Scopes          []string  `json:"scopes"`
	Enabled         bool      `json:"enabled"`
	AutoProvision   bool      `json:"auto_provision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SSOProviderInput is used when creating a new SSO provider.
type SSOProviderInput struct {
	Name            string
	ProviderType    string
	IssuerURL       string
	ClientID        string
	ClientSecretEnc string // already encrypted by caller
	Scopes          []string
	Enabled         bool
	AutoProvision   bool
}

// SSOProviderUpdate holds mutable fields for an admin SSO provider update.
// nil pointer = don't update that field.
type SSOProviderUpdate struct {
	Name            *string
	IssuerURL       *string
	ClientID        *string
	ClientSecretEnc *string  // nil = don't update secret
	Scopes          []string // nil = don't update
	Enabled         *bool
	AutoProvision   *bool
}

// PasswordPolicy is the singleton row controlling password requirements.
type PasswordPolicy struct {
	MinLength               int       `json:"min_length"`
	RequireUppercase        bool      `json:"require_uppercase"`
	RequireLowercase        bool      `json:"require_lowercase"`
	RequireNumber           bool      `json:"require_number"`
	RequireSpecial          bool      `json:"require_special"`
	MaxFailedAttempts       int       `json:"max_failed_attempts"`
	LockoutDurationMinutes  int       `json:"lockout_duration_minutes"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// PasswordPolicyUpdate holds mutable fields; nil = don't update.
type PasswordPolicyUpdate struct {
	MinLength               *int
	RequireUppercase        *bool
	RequireLowercase        *bool
	RequireNumber           *bool
	RequireSpecial          *bool
	MaxFailedAttempts       *int
	LockoutDurationMinutes  *int
}

// APIToken is a personal access token for programmatic API access.
// The raw token is shown once on creation; only the SHA-256 hash is stored.
// StoredHash is populated by GetAPITokenWithUser for constant-time comparison.
type APIToken struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Name        string     `json:"name"`
	StoredHash  string     `json:"-"` // populated by lookup queries; never serialised
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
