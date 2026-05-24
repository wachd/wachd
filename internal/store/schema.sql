-- Wachd database schema — runs automatically on startup via store.Migrate()
-- All statements use IF NOT EXISTS so this is safe to run repeatedly.
-- Uses gen_random_uuid() (built-in since PostgreSQL 13) — no extensions required.

CREATE TABLE IF NOT EXISTS teams (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name           VARCHAR(255) NOT NULL,
    webhook_secret VARCHAR(255) NOT NULL UNIQUE,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_teams_webhook_secret ON teams(webhook_secret);

CREATE TABLE IF NOT EXISTS users (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id    UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name       VARCHAR(255) NOT NULL,
    email      VARCHAR(255) NOT NULL,
    phone      VARCHAR(50),
    role       VARCHAR(50)  NOT NULL DEFAULT 'responder',
    created_at TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_team_id ON users(team_id);
CREATE INDEX IF NOT EXISTS idx_users_email   ON users(email);

CREATE TABLE IF NOT EXISTS incidents (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id       UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    title         VARCHAR(500) NOT NULL,
    message       TEXT,
    severity      VARCHAR(50)  NOT NULL DEFAULT 'unknown',
    status        VARCHAR(50)  NOT NULL DEFAULT 'open',
    source        VARCHAR(100) NOT NULL,
    alert_payload JSONB        NOT NULL,
    context       JSONB,
    analysis      JSONB,
    fired_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged_at  TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ,
    snoozed_until    TIMESTAMPTZ,
    escalation_step  INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_to   UUID REFERENCES users(id)
);
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS escalation_step INT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_incidents_team_id     ON incidents(team_id);
CREATE INDEX IF NOT EXISTS idx_incidents_status      ON incidents(status);
CREATE INDEX IF NOT EXISTS idx_incidents_fired_at    ON incidents(fired_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_assigned_to ON incidents(assigned_to);

CREATE TABLE IF NOT EXISTS schedules (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id         UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    rotation_config JSONB        NOT NULL,
    enabled         BOOLEAN      NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_schedules_team_id ON schedules(team_id);
CREATE INDEX IF NOT EXISTS idx_schedules_enabled ON schedules(enabled);

-- SSO identity: one row per person, provider-scoped, not team-scoped
CREATE TABLE IF NOT EXISTS sso_identities (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    provider    VARCHAR(50)  NOT NULL,           -- entra | google | okta
    provider_id VARCHAR(500) NOT NULL,           -- oid claim from Entra
    email       VARCHAR(255) NOT NULL,
    name        VARCHAR(255) NOT NULL,
    avatar_url  VARCHAR(500),
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE(provider, provider_id)
);

-- Team access: SSO identity → team (many-to-many with role)
CREATE TABLE IF NOT EXISTS sso_team_access (
    identity_id UUID        NOT NULL REFERENCES sso_identities(id) ON DELETE CASCADE,
    team_id     UUID        NOT NULL REFERENCES teams(id)          ON DELETE CASCADE,
    role        VARCHAR(50) NOT NULL DEFAULT 'viewer',
    PRIMARY KEY (identity_id, team_id)
);

CREATE INDEX IF NOT EXISTS idx_sso_team_access_team ON sso_team_access(team_id);

-- Entra group → wachd team mapping
CREATE TABLE IF NOT EXISTS group_mappings (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    provider    VARCHAR(50)  NOT NULL DEFAULT 'entra',
    group_id    VARCHAR(500) NOT NULL,           -- Entra group object ID
    group_name  VARCHAR(255),                    -- human-readable label
    team_id     UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    role        VARCHAR(50)  NOT NULL DEFAULT 'viewer',
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE(provider, group_id, team_id)
);

CREATE INDEX IF NOT EXISTS idx_group_mappings_group ON group_mappings(provider, group_id);

-- Sessions (Redis is authoritative/TTL; this table is audit trail only)
CREATE TABLE IF NOT EXISTS sessions (
    id          UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id UUID     NOT NULL REFERENCES sso_identities(id) ON DELETE CASCADE,
    token_hash  CHAR(64) NOT NULL UNIQUE,        -- SHA-256 hex of session token
    expires_at  TIMESTAMPTZ NOT NULL,
    ip_address  VARCHAR(45),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash);

-- ============================================================
-- Enterprise Auth — Phase 0 additions
-- ============================================================

-- Password policy singleton (one row, always ID=1)
CREATE TABLE IF NOT EXISTS password_policy (
    id                       INT         PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    min_length               INT         NOT NULL DEFAULT 12,
    require_uppercase        BOOLEAN     NOT NULL DEFAULT true,
    require_lowercase        BOOLEAN     NOT NULL DEFAULT true,
    require_number           BOOLEAN     NOT NULL DEFAULT true,
    require_special          BOOLEAN     NOT NULL DEFAULT true,
    max_failed_attempts      INT         NOT NULL DEFAULT 5,
    lockout_duration_minutes INT         NOT NULL DEFAULT 30,
    updated_at               TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
INSERT INTO password_policy (id) VALUES (1) ON CONFLICT DO NOTHING;

-- Local (non-SSO) users — global auth identities, not team-scoped
CREATE TABLE IF NOT EXISTS local_users (
    id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    username              VARCHAR(100) NOT NULL UNIQUE,
    email                 VARCHAR(255) NOT NULL UNIQUE,
    name                  VARCHAR(255) NOT NULL,
    password_hash         TEXT         NOT NULL,
    is_superadmin         BOOLEAN      NOT NULL DEFAULT false,
    is_active             BOOLEAN      NOT NULL DEFAULT true,
    force_password_change BOOLEAN      NOT NULL DEFAULT false,
    failed_login_attempts INT          NOT NULL DEFAULT 0,
    locked_until          TIMESTAMPTZ,
    last_login_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_local_users_username ON local_users(username);
CREATE INDEX IF NOT EXISTS idx_local_users_email    ON local_users(email);

-- Local groups (superadmin-managed; independent of SSO groups)
CREATE TABLE IF NOT EXISTS local_groups (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- Local group membership (user → group many-to-many)
CREATE TABLE IF NOT EXISTS local_group_members (
    group_id   UUID      NOT NULL REFERENCES local_groups(id) ON DELETE CASCADE,
    user_id    UUID      NOT NULL REFERENCES local_users(id)  ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_local_group_members_user ON local_group_members(user_id);

-- Local group → wachd team access (mirrors sso_team_access semantics)
CREATE TABLE IF NOT EXISTS local_group_access (
    group_id UUID        NOT NULL REFERENCES local_groups(id) ON DELETE CASCADE,
    team_id  UUID        NOT NULL REFERENCES teams(id)        ON DELETE CASCADE,
    role     VARCHAR(50) NOT NULL DEFAULT 'viewer',
    PRIMARY KEY (group_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_local_group_access_team ON local_group_access(team_id);

-- SSO provider config — DB-stored, AES-256-GCM encrypted client secret
CREATE TABLE IF NOT EXISTS sso_providers (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name              VARCHAR(100) NOT NULL,
    provider_type     VARCHAR(50)  NOT NULL DEFAULT 'oidc',
    issuer_url        VARCHAR(500) NOT NULL,
    client_id         VARCHAR(255) NOT NULL,
    client_secret_enc TEXT         NOT NULL,
    scopes            TEXT[]       NOT NULL DEFAULT ARRAY['openid','profile','email'],
    enabled           BOOLEAN      NOT NULL DEFAULT true,
    auto_provision    BOOLEAN      NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sso_providers_enabled ON sso_providers(enabled);

-- Extend sessions table for local-user sessions
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS local_user_id UUID REFERENCES local_users(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS auth_type     VARCHAR(20) NOT NULL DEFAULT 'sso';
ALTER TABLE sessions ALTER COLUMN identity_id DROP NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_local_user ON sessions(local_user_id);

-- Allow SSO identities to be flagged as superadmin
ALTER TABLE sso_identities
    ADD COLUMN IF NOT EXISTS is_superadmin BOOLEAN NOT NULL DEFAULT false;

-- Link group_mappings to the sso_providers table (nullable for legacy rows)
ALTER TABLE group_mappings
    ADD COLUMN IF NOT EXISTS sso_provider_id UUID REFERENCES sso_providers(id) ON DELETE SET NULL;

-- ============================================================

-- API tokens (personal access tokens — Bearer auth for programmatic access)
CREATE TABLE IF NOT EXISTS api_tokens (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID         NOT NULL REFERENCES local_users(id) ON DELETE CASCADE,
    name         VARCHAR(255) NOT NULL,
    token_hash   CHAR(64)     NOT NULL UNIQUE,  -- SHA-256 hex of the raw token
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,                      -- NULL = never expires
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash);

-- ============================================================

-- ============================================================
-- On-Call Scheduling — Phase 1 additions
-- ============================================================

-- Temporary overrides: replace the scheduled user for a time window
CREATE TABLE IF NOT EXISTS schedule_overrides (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id UUID         NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    team_id     UUID         NOT NULL,  -- denormalized for fast team-scoped queries
    start_at    TIMESTAMPTZ    NOT NULL,
    end_at      TIMESTAMPTZ    NOT NULL,
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT,
    created_by  UUID         NOT NULL,  -- local_users.id or users.id of the creator
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_schedule_overrides_schedule ON schedule_overrides(schedule_id);
CREATE INDEX IF NOT EXISTS idx_schedule_overrides_team     ON schedule_overrides(team_id);
CREATE INDEX IF NOT EXISTS idx_schedule_overrides_window   ON schedule_overrides(start_at, end_at);

-- Escalation policy: per-team ordered chain with ack-timeout per layer
CREATE TABLE IF NOT EXISTS escalation_policies (
    id         UUID      PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id    UUID      NOT NULL UNIQUE REFERENCES teams(id) ON DELETE CASCADE,
    config     JSONB     NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_escalation_policies_team ON escalation_policies(team_id);

-- ============================================================
-- Identity refactor — phone on auth tables, drop phantom FK constraints
-- ============================================================

-- Phone for local users (for SMS/voice on-call paging)
ALTER TABLE local_users ADD COLUMN IF NOT EXISTS phone VARCHAR(50);

-- Phone for SSO identities (admin can set; not overwritten by SSO sync)
ALTER TABLE sso_identities ADD COLUMN IF NOT EXISTS phone VARCHAR(50);

-- Drop FK that tied incidents.assigned_to to the old on-call contacts table.
-- assigned_to now holds any identity UUID (local_users or sso_identities).
ALTER TABLE incidents DROP CONSTRAINT IF EXISTS incidents_assigned_to_fkey;

-- Drop FK that tied schedule_overrides.user_id to the old contacts table.
-- user_id now holds any identity UUID.
ALTER TABLE schedule_overrides DROP CONSTRAINT IF EXISTS schedule_overrides_user_id_fkey;

-- ============================================================

CREATE TABLE IF NOT EXISTS team_config (
    team_id                UUID         PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    slack_webhook_url      VARCHAR(500),
    slack_channel          VARCHAR(100),
    slack_bot_token        VARCHAR(255),
    github_token_encrypted TEXT,
    github_repos           JSONB,
    grafana_mcp_url             VARCHAR(500),
    grafana_mcp_token_encrypted TEXT,
    prometheus_endpoint         VARCHAR(500),
    loki_endpoint               VARCHAR(500),
    dynatrace_endpoint          VARCHAR(500),
    dynatrace_token_encrypted   TEXT,
    splunk_endpoint             VARCHAR(500),
    splunk_token_encrypted      TEXT,
    created_at             TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- Idempotent migration: add Dynatrace and Splunk columns if they don't exist yet.
-- Safe to run on existing databases (no-op if columns already present).
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS dynatrace_endpoint        VARCHAR(500);
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS dynatrace_token_encrypted TEXT;
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS splunk_endpoint            VARCHAR(500);
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS splunk_token_encrypted    TEXT;
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS grafana_mcp_url            VARCHAR(500);
ALTER TABLE team_config ADD COLUMN IF NOT EXISTS grafana_mcp_token_encrypted TEXT;

-- Idempotent migration: move AI backend config out of team_config (per-team) into
-- system_config (platform-wide). Drop the columns so the schema stays clean.
ALTER TABLE team_config DROP COLUMN IF EXISTS ai_backend;
ALTER TABLE team_config DROP COLUMN IF EXISTS ai_model;

-- ============================================================
-- User-level notification rules
-- Each user configures their own channels and optional delay per event type.
-- ============================================================
CREATE TABLE IF NOT EXISTS user_notification_rules (
    id            UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID    NOT NULL,        -- local_users.id or sso_identities.id
    user_source   TEXT    NOT NULL,        -- 'local' | 'sso'
    event_type    TEXT    NOT NULL,        -- 'new_alert' | 'ack' | 'resolve'
    channel       TEXT    NOT NULL,        -- 'email' | 'sms' | 'voice' | 'slack'
    delay_minutes INT     NOT NULL DEFAULT 0,
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, user_source, event_type, channel, delay_minutes)
);
CREATE INDEX IF NOT EXISTS idx_user_notif_rules_user ON user_notification_rules(user_id, user_source);

-- Queued delayed channel sends (e.g. voice call after 10 min if still unacked)
CREATE TABLE IF NOT EXISTS pending_notifications (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id  UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    team_id      UUID NOT NULL,
    user_id      UUID NOT NULL,
    user_source  TEXT NOT NULL,
    channel      TEXT NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    sent_at      TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pending_notif_fire ON pending_notifications(scheduled_at)
    WHERE sent_at IS NULL AND cancelled_at IS NULL;

-- ============================================================
-- Team graph configuration
-- Controls whether resolved incidents are written to the knowledge graph and
-- what minimum similarity score should be used for retrieval.
-- ============================================================
CREATE TABLE IF NOT EXISTS team_graph_config (
    team_id              UUID PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    enabled              BOOLEAN     NOT NULL DEFAULT true,
    min_similarity_score FLOAT       NOT NULL DEFAULT 0.12,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Service dependencies
-- Teams declare which services a given service depends on.
-- When an alert fires for a service, the collector also pulls
-- logs and metrics for each declared dependency using the team's
-- existing configured connectors.
-- ============================================================
CREATE TABLE IF NOT EXISTS service_dependencies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    service      TEXT NOT NULL,
    depends_on   TEXT NOT NULL,
    label        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(team_id, service, depends_on)
);
CREATE INDEX IF NOT EXISTS idx_service_deps_team_service ON service_dependencies(team_id, service);

-- ============================================================
-- Platform-wide system configuration (superadmin only)
-- Singleton row: always use id = 1.
-- ============================================================

CREATE TABLE IF NOT EXISTS system_config (
    id          INT          PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    ai_backend  VARCHAR(50)  NOT NULL DEFAULT 'ollama',
    ai_model    VARCHAR(100),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_by  UUID         REFERENCES local_users(id) ON DELETE SET NULL
);

-- ============================================================
-- Incident Knowledge Graph
-- Nodes: incidents, deployments, services, alert rules.
-- Edges: directed relationships between nodes.
-- All rows are team-scoped — no cross-team access is possible.
--
-- Two-phase write-back: nodes (and edges) start as 'pending' when written at
-- alert-fire time. PromoteNode() flips them to 'permanent' on incident resolve.
-- Every FindSimilar and GetSubgraph neighbour query must filter
-- WHERE status = 'permanent' — that predicate is the contamination guarantee.
-- The root node of GetSubgraph is returned regardless of status so that
-- responders can view an active incident's own graph while it is still open.
--
-- Adding new node or edge types: add the value to the CHECK constraint below
-- AND add a constant to the NodeType / EdgeType Go types in internal/graph/store.go.
-- Never store new types as free-form strings in the properties column.
-- ============================================================

CREATE TABLE IF NOT EXISTS graph_nodes (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    type        VARCHAR(50)  NOT NULL
                             CHECK (type IN ('incident', 'deployment', 'service', 'alert')),
    label       TEXT         NOT NULL,
    external_id TEXT,                    -- incidents.id, commit hash, service name, etc.
    properties  JSONB,
    status      VARCHAR(20)  NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'permanent')),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Required for composite FK from graph_edges — enforces team isolation at
    -- the DB level so an edge in team A cannot reference a node in team B.
    UNIQUE(team_id, id)
);

CREATE INDEX IF NOT EXISTS idx_graph_nodes_team     ON graph_nodes(team_id);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_type     ON graph_nodes(team_id, type);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_status   ON graph_nodes(team_id, status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_graph_nodes_external
    ON graph_nodes(team_id, type, external_id)
    WHERE external_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS graph_edges (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID         NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    from_id     UUID         NOT NULL,
    to_id       UUID         NOT NULL,
    type        VARCHAR(50)  NOT NULL
                             CHECK (type IN ('caused_by', 'affects', 'similar_to', 'triggered')),
    status      VARCHAR(20)  NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'permanent')),
    weight      FLOAT        NOT NULL DEFAULT 1.0,
    properties  JSONB,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Composite FKs enforce that both endpoints belong to the same team as the
    -- edge. A plain FK on from_id / to_id alone would allow cross-team edges.
    FOREIGN KEY (team_id, from_id) REFERENCES graph_nodes(team_id, id) ON DELETE CASCADE,
    FOREIGN KEY (team_id, to_id)   REFERENCES graph_nodes(team_id, id) ON DELETE CASCADE,
    UNIQUE(team_id, from_id, to_id, type)
);

CREATE INDEX IF NOT EXISTS idx_graph_edges_team ON graph_edges(team_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_from ON graph_edges(from_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_to   ON graph_edges(to_id);

-- ============================================================
-- Issue #25: migrate legacy TIMESTAMP columns to TIMESTAMPTZ
-- ============================================================
-- Existing databases may still have older columns created as TIMESTAMP
-- WITHOUT TIME ZONE. Go already writes UTC values and the DB connection forces
-- UTC sessions, so reinterpret existing values as UTC during the type change.
--
-- The information_schema guard keeps this migration idempotent: once a column
-- is already TIMESTAMPTZ, it is skipped on future startup migrations.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT c.table_name, c.column_name
        FROM (VALUES
            ('teams', 'created_at'),
            ('teams', 'updated_at'),

            ('users', 'created_at'),
            ('users', 'updated_at'),

            ('incidents', 'fired_at'),
            ('incidents', 'acknowledged_at'),
            ('incidents', 'resolved_at'),
            ('incidents', 'snoozed_until'),
            ('incidents', 'created_at'),
            ('incidents', 'updated_at'),

            ('schedules', 'created_at'),
            ('schedules', 'updated_at'),

            ('sso_identities', 'created_at'),
            ('sso_identities', 'updated_at'),

            ('sessions', 'expires_at'),
            ('sessions', 'created_at'),

            ('password_policy', 'updated_at'),

            ('local_users', 'locked_until'),
            ('local_users', 'last_login_at'),
            ('local_users', 'created_at'),
            ('local_users', 'updated_at'),

            ('local_groups', 'created_at'),
            ('local_groups', 'updated_at'),

            ('local_group_members', 'created_at'),

            ('group_mappings', 'created_at'),

            ('sso_providers', 'created_at'),
            ('sso_providers', 'updated_at'),

            ('api_tokens', 'last_used_at'),
            ('api_tokens', 'expires_at'),
            ('api_tokens', 'created_at'),

            ('schedule_overrides', 'start_at'),
            ('schedule_overrides', 'end_at'),
            ('schedule_overrides', 'created_at'),

            ('escalation_policies', 'updated_at'),

            ('team_config', 'created_at'),
            ('team_config', 'updated_at'),

            ('system_config', 'updated_at')
        ) AS issue25_legacy_timestamp_columns(table_name, column_name)
        JOIN information_schema.columns c
          ON c.table_schema = current_schema()
         AND c.table_name = issue25_legacy_timestamp_columns.table_name
         AND c.column_name = issue25_legacy_timestamp_columns.column_name
        WHERE c.data_type = 'timestamp without time zone'
    LOOP
        EXECUTE format(
            'ALTER TABLE %I ALTER COLUMN %I TYPE TIMESTAMPTZ USING %I AT TIME ZONE ''UTC''',
            r.table_name,
            r.column_name,
            r.column_name
        );
    END LOOP;
END $$;
