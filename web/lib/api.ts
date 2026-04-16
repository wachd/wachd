// API client for the Wachd backend

import type { Incident, TeamMember, Schedule, OnCallUser, ScheduleOverride } from './types';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '';

class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

async function fetchApi<T>(endpoint: string, options?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE_URL}${endpoint}`, {
    ...options,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  });

  if (!response.ok) {
    const error = await response.text();
    throw new ApiError(response.status, error || response.statusText);
  }

  const text = await response.text();
  if (!text) return undefined as unknown as T;
  return JSON.parse(text);
}

export interface TeamConfigPublic {
  team_id: string;
  webhook_secret: string;
  slack_webhook_url?: string;
  slack_channel?: string;
  github_token_set: boolean;
  github_repos?: string[];
  prometheus_endpoint?: string;
  loki_endpoint?: string;
  ai_backend: string;
  ai_model?: string;
}

export interface TeamConfigInput {
  slack_webhook_url?: string | null;
  slack_channel?: string | null;
  github_token?: string;
  github_repos?: string[];
  prometheus_endpoint?: string | null;
  loki_endpoint?: string | null;
  ai_backend?: string;
  ai_model?: string | null;
}

export interface EscalationConfig {
  escalation_timeout_minutes: number;
  repeat_interval_minutes: number;
  max_repeats: number;
}

export const api = {
  // Incidents
  incidents: {
    list: async (teamId: string) => {
      const response = await fetchApi<{ incidents: Incident[]; count: number }>(
        `/api/v1/teams/${teamId}/incidents`
      );
      return response.incidents || [];
    },

    get: (teamId: string, incidentId: string) =>
      fetchApi<Incident>(`/api/v1/teams/${teamId}/incidents/${incidentId}`),

    acknowledge: (teamId: string, incidentId: string) =>
      fetchApi(`/api/v1/teams/${teamId}/incidents/${incidentId}/ack`, {
        method: 'POST',
      }),

    resolve: (teamId: string, incidentId: string) =>
      fetchApi(`/api/v1/teams/${teamId}/incidents/${incidentId}/resolve`, {
        method: 'POST',
      }),

    reopen: (teamId: string, incidentId: string) =>
      fetchApi(`/api/v1/teams/${teamId}/incidents/${incidentId}/reopen`, {
        method: 'POST',
      }),

    snooze: (teamId: string, incidentId: string, minutes: number) =>
      fetchApi(`/api/v1/teams/${teamId}/incidents/${incidentId}/snooze`, {
        method: 'POST',
        body: JSON.stringify({ minutes }),
      }),
  },

  // On-call schedule
  schedule: {
    get: async (teamId: string): Promise<Schedule | null> => {
      const data = await fetchApi<{ configured?: boolean } & Schedule>(
        `/api/v1/teams/${teamId}/schedule`
      );
      if (!data || data.configured === false) return null;
      return data as Schedule;
    },

    upsert: (teamId: string, payload: { name?: string; rotation_config: Record<string, any>; enabled?: boolean }) =>
      fetchApi<Schedule>(`/api/v1/teams/${teamId}/schedule`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      }),

    getCurrentOnCall: async (teamId: string): Promise<OnCallUser | null> => {
      const data = await fetchApi<{ configured?: boolean } & OnCallUser>(
        `/api/v1/teams/${teamId}/oncall/now`
      );
      if (!data || data.configured === false) return null;
      return data as OnCallUser;
    },

    listOverrides: async (teamId: string): Promise<ScheduleOverride[]> => {
      const data = await fetchApi<{ overrides: ScheduleOverride[]; count: number }>(
        `/api/v1/teams/${teamId}/schedule/overrides`
      );
      return data.overrides || [];
    },

    createOverride: (teamId: string, payload: { user_id: string; start_at: string; end_at: string; reason?: string }) =>
      fetchApi<ScheduleOverride>(`/api/v1/teams/${teamId}/schedule/overrides`, {
        method: 'POST',
        body: JSON.stringify(payload),
      }),

    deleteOverride: (teamId: string, overrideId: string) =>
      fetchApi(`/api/v1/teams/${teamId}/schedule/overrides/${overrideId}`, {
        method: 'DELETE',
      }),

    getTimeline: (teamId: string, from: string, days: number) =>
      fetchApi<{
        schedule_name: string;
        layer_names: string[];
        from: string;
        days: number;
        entries: Array<{
          date: string;
          layers: Array<{ layer_name: string; user_id: string; user_name: string }>;
          override?: { id: string; user_id: string; user_name: string; reason?: string; start_at: string; end_at: string };
          final_user_id: string;
          final_user_name: string;
        }>;
      }>(`/api/v1/teams/${teamId}/oncall/timeline?from=${from}&days=${days}`),
  },

  // Team members — sourced from group access, not a separate contacts table.
  // Admin can only update phone number. Add/remove members via Admin → Groups.
  members: {
    list: async (teamId: string): Promise<TeamMember[]> => {
      const data = await fetchApi<{ members: TeamMember[]; count: number }>(
        `/api/v1/teams/${teamId}/members`
      );
      return data.members || [];
    },

    updatePhone: (teamId: string, userId: string, source: 'local' | 'sso', phone: string | null) =>
      fetchApi<TeamMember>(`/api/v1/teams/${teamId}/members/${userId}`, {
        method: 'PUT',
        body: JSON.stringify({ source, phone }),
      }),
  },

  // Health check
  health: () => fetchApi<{ status: string }>('/api/v1/health'),

  // Auth
  auth: {
    me: () =>
      fetchApi<{
        identity_id: string;
        email: string;
        name: string;
        avatar_url?: string;
        team_ids: string[];
        roles: Record<string, string>;
        auth_type?: string;
        is_superadmin?: boolean;
        force_password_change?: boolean;
      }>('/auth/me'),

    logout: () =>
      fetchApi('/auth/logout', { method: 'POST' }),

    localLogin: (username: string, password: string) =>
      fetchApi<{ force_password_change: boolean; is_superadmin: boolean }>(
        '/auth/local/login',
        { method: 'POST', body: JSON.stringify({ username, password }) }
      ),

    changePassword: (currentPassword: string, newPassword: string) =>
      fetchApi('/auth/local/change-password', {
        method: 'POST',
        body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
      }),
  },

  // Admin (superadmin only)
  admin: {
    users: {
      list: () => fetchApi<{ users: Record<string, unknown>[]; count: number }>('/api/v1/admin/users'),
      create: (data: { username: string; email: string; name: string; password: string; is_superadmin?: boolean }) =>
        fetchApi('/api/v1/admin/users', { method: 'POST', body: JSON.stringify(data) }),
      get: (id: string) => fetchApi(`/api/v1/admin/users/${id}`),
      update: (id: string, data: { email?: string; name?: string; is_active?: boolean }) =>
        fetchApi(`/api/v1/admin/users/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
      delete: (id: string) => fetchApi(`/api/v1/admin/users/${id}`, { method: 'DELETE' }),
      resetPassword: (id: string, newPassword: string) =>
        fetchApi(`/api/v1/admin/users/${id}/reset-password`, {
          method: 'POST',
          body: JSON.stringify({ new_password: newPassword }),
        }),
    },
    groups: {
      list: () => fetchApi<{ groups: Record<string, unknown>[]; count: number }>('/api/v1/admin/groups'),
      create: (name: string, description?: string) =>
        fetchApi('/api/v1/admin/groups', { method: 'POST', body: JSON.stringify({ name, description }) }),
      delete: (id: string) => fetchApi(`/api/v1/admin/groups/${id}`, { method: 'DELETE' }),
      listMembers: (id: string) => fetchApi(`/api/v1/admin/groups/${id}/members`),
      addMember: (id: string, userId: string) =>
        fetchApi(`/api/v1/admin/groups/${id}/members`, { method: 'POST', body: JSON.stringify({ user_id: userId }) }),
      removeMember: (id: string, userId: string) =>
        fetchApi(`/api/v1/admin/groups/${id}/members/${userId}`, { method: 'DELETE' }),
      listAccess: (id: string) => fetchApi(`/api/v1/admin/groups/${id}/access`),
      grantAccess: (id: string, teamId: string, role: string) =>
        fetchApi(`/api/v1/admin/groups/${id}/access`, { method: 'POST', body: JSON.stringify({ team_id: teamId, role }) }),
      revokeAccess: (id: string, teamId: string) =>
        fetchApi(`/api/v1/admin/groups/${id}/access/${teamId}`, { method: 'DELETE' }),
    },
    sso: {
      list: () => fetchApi<{ providers: Record<string, unknown>[]; count: number }>('/api/v1/admin/sso/providers'),
      create: (data: { name: string; issuer_url: string; client_id: string; client_secret: string; scopes?: string[]; enabled?: boolean; auto_provision?: boolean }) =>
        fetchApi('/api/v1/admin/sso/providers', { method: 'POST', body: JSON.stringify(data) }),
      get: (id: string) => fetchApi(`/api/v1/admin/sso/providers/${id}`),
      update: (id: string, data: Record<string, unknown>) =>
        fetchApi(`/api/v1/admin/sso/providers/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
      delete: (id: string) => fetchApi(`/api/v1/admin/sso/providers/${id}`, { method: 'DELETE' }),
      test: (id: string) => fetchApi(`/api/v1/admin/sso/providers/${id}/test`, { method: 'POST' }),
    },
    teams: {
      list: () => fetchApi('/api/v1/admin/teams'),
      create: (name: string) =>
        fetchApi('/api/v1/admin/teams', { method: 'POST', body: JSON.stringify({ name }) }),
      delete: (id: string) => fetchApi(`/api/v1/admin/teams/${id}`, { method: 'DELETE' }),
    },
    groupMappings: {
      list: () => fetchApi('/api/v1/admin/group-mappings'),
      create: (data: { provider_id: string; group_id: string; group_name?: string; team_id: string; role: string }) =>
        fetchApi('/api/v1/admin/group-mappings', { method: 'POST', body: JSON.stringify(data) }),
      delete: (id: string) => fetchApi(`/api/v1/admin/group-mappings/${id}`, { method: 'DELETE' }),
    },
    passwordPolicy: {
      get: () => fetchApi('/api/v1/admin/password-policy'),
      update: (data: Record<string, unknown>) =>
        fetchApi('/api/v1/admin/password-policy', { method: 'PUT', body: JSON.stringify(data) }),
    },
    tokens: {
      list: () => fetchApi<{ tokens: { id: string; name: string; last_used_at?: string; expires_at?: string; created_at: string }[]; count: number }>('/api/v1/admin/tokens'),
      create: (name: string) =>
        fetchApi<{ id: string; name: string; token: string; created_at: string }>('/api/v1/admin/tokens', {
          method: 'POST',
          body: JSON.stringify({ name }),
        }),
      delete: (id: string) => fetchApi(`/api/v1/admin/tokens/${id}`, { method: 'DELETE' }),
    },
  },

  // Team configuration (data sources, notifications, AI backend)
  teamConfig: {
    get: (teamId: string) =>
      fetchApi<TeamConfigPublic>(`/api/v1/teams/${teamId}/config`),
    update: (teamId: string, data: TeamConfigInput) =>
      fetchApi<TeamConfigPublic>(`/api/v1/teams/${teamId}/config`, {
        method: 'PUT',
        body: JSON.stringify(data),
      }),
  },

  // Escalation policy
  escalation: {
    get: (teamId: string) =>
      fetchApi<{ config: EscalationConfig | null; updated_at?: string }>(
        `/api/v1/teams/${teamId}/escalation`
      ),
    update: (teamId: string, config: EscalationConfig) =>
      fetchApi<{ config: EscalationConfig; updated_at: string }>(
        `/api/v1/teams/${teamId}/escalation`,
        { method: 'PUT', body: JSON.stringify({ config }) }
      ),
  },
};

