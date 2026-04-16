'use client';

import { useEffect, useState, useCallback } from 'react';
import { useSession } from '@/lib/session-context';
import { api, type TeamConfigPublic, type TeamConfigInput, type EscalationConfig } from '@/lib/api';
import type { TeamMember } from '@/lib/types';

type Tab = 'general' | 'datasources' | 'notifications' | 'members' | 'escalation';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '';

export default function SettingsPage() {
  const { session, loading: sessionLoading, primaryTeamId } = useSession();

  const isAdmin =
    session?.isSuperAdmin ||
    (primaryTeamId != null && session?.roles[primaryTeamId] === 'admin');

  const [activeTab, setActiveTab] = useState<Tab>('general');

  // Config state
  const [config, setConfig] = useState<TeamConfigPublic | null>(null);
  const [configLoading, setConfigLoading] = useState(true);
  const [configError, setConfigError] = useState<string | null>(null);

  // Data sources form
  const [githubToken, setGithubToken] = useState('');
  const [githubRepos, setGithubRepos] = useState('');
  const [prometheusEndpoint, setPrometheusEndpoint] = useState('');
  const [lokiEndpoint, setLokiEndpoint] = useState('');
  const [aiBackend, setAiBackend] = useState('ollama');
  const [aiModel, setAiModel] = useState('');

  // Notifications form
  const [slackWebhookUrl, setSlackWebhookUrl] = useState('');
  const [slackChannel, setSlackChannel] = useState('');

  // Members state
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [membersLoaded, setMembersLoaded] = useState(false);
  const [membersLoading, setMembersLoading] = useState(false);
  const [editingPhoneId, setEditingPhoneId] = useState<string | null>(null);
  const [editingPhone, setEditingPhone] = useState('');

  // Escalation state
  const [escalation, setEscalation] = useState<EscalationConfig>({
    escalation_timeout_minutes: 10,
    repeat_interval_minutes: 30,
    max_repeats: 3,
  });
  const [escalationLoaded, setEscalationLoaded] = useState(false);
  const [escalationLoading, setEscalationLoading] = useState(false);

  // Save state (shared)
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [copiedWebhook, setCopiedWebhook] = useState(false);

  // Load team config on mount
  useEffect(() => {
    if (!primaryTeamId) return;
    setConfigLoading(true);
    api.teamConfig
      .get(primaryTeamId)
      .then((cfg) => {
        setConfig(cfg);
        setGithubRepos((cfg.github_repos ?? []).join('\n'));
        setPrometheusEndpoint(cfg.prometheus_endpoint ?? '');
        setLokiEndpoint(cfg.loki_endpoint ?? '');
        setAiBackend(cfg.ai_backend ?? 'ollama');
        setAiModel(cfg.ai_model ?? '');
        setSlackWebhookUrl(cfg.slack_webhook_url ?? '');
        setSlackChannel(cfg.slack_channel ?? '');
      })
      .catch(() => setConfigError('Failed to load team configuration'))
      .finally(() => setConfigLoading(false));
  }, [primaryTeamId]);

  // Load members lazily when members tab is opened
  useEffect(() => {
    if (activeTab !== 'members' || !primaryTeamId || membersLoaded) return;
    setMembersLoading(true);
    api.members
      .list(primaryTeamId)
      .then((data) => { setMembers(data); setMembersLoaded(true); })
      .catch(() => setMembersLoaded(true))
      .finally(() => setMembersLoading(false));
  }, [activeTab, primaryTeamId, membersLoaded]);

  // Load escalation lazily when escalation tab is opened
  useEffect(() => {
    if (activeTab !== 'escalation' || !primaryTeamId || escalationLoaded) return;
    setEscalationLoading(true);
    api.escalation
      .get(primaryTeamId)
      .then((data) => { if (data.config) setEscalation(data.config); setEscalationLoaded(true); })
      .catch(() => setEscalationLoaded(true))
      .finally(() => setEscalationLoading(false));
  }, [activeTab, primaryTeamId, escalationLoaded]);

  const flashSuccess = () => {
    setSaveSuccess(true);
    setTimeout(() => setSaveSuccess(false), 3000);
  };

  const saveDataSources = useCallback(async () => {
    if (!primaryTeamId) return;
    setSaving(true);
    setSaveError(null);
    const repos = githubRepos
      .split('\n')
      .map((r) => r.trim())
      .filter(Boolean);
    const input: TeamConfigInput = {
      github_repos: repos,
      prometheus_endpoint: prometheusEndpoint || null,
      loki_endpoint: lokiEndpoint || null,
      ai_backend: aiBackend,
      ai_model: aiModel || null,
    };
    if (githubToken) input.github_token = githubToken;
    try {
      const updated = await api.teamConfig.update(primaryTeamId, input);
      setConfig(updated);
      setGithubToken('');
      flashSuccess();
    } catch (e: unknown) {
      setSaveError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [primaryTeamId, githubToken, githubRepos, prometheusEndpoint, lokiEndpoint, aiBackend, aiModel]);

  const saveNotifications = useCallback(async () => {
    if (!primaryTeamId) return;
    setSaving(true);
    setSaveError(null);
    try {
      const updated = await api.teamConfig.update(primaryTeamId, {
        slack_webhook_url: slackWebhookUrl || null,
        slack_channel: slackChannel || null,
      });
      setConfig(updated);
      flashSuccess();
    } catch (e: unknown) {
      setSaveError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [primaryTeamId, slackWebhookUrl, slackChannel]);

  const saveEscalation = useCallback(async () => {
    if (!primaryTeamId) return;
    setSaving(true);
    setSaveError(null);
    try {
      await api.escalation.update(primaryTeamId, escalation);
      flashSuccess();
    } catch (e: unknown) {
      setSaveError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  }, [primaryTeamId, escalation]);

  const savePhone = useCallback(
    async (member: TeamMember) => {
      if (!primaryTeamId) return;
      setSaving(true);
      setSaveError(null);
      try {
        const updated = await api.members.updatePhone(
          primaryTeamId,
          member.id,
          member.source,
          editingPhone.trim() || null
        );
        setMembers((prev) => prev.map((m) => (m.id === updated.id ? updated : m)));
        setEditingPhoneId(null);
        flashSuccess();
      } catch (e: unknown) {
        setSaveError(e instanceof Error ? e.message : 'Save failed');
      } finally {
        setSaving(false);
      }
    },
    [primaryTeamId, editingPhone]
  );

  const copyWebhookUrl = () => {
    if (!config) return;
    const url = `${API_BASE_URL}/api/v1/webhook/${config.team_id}/${config.webhook_secret}`;
    navigator.clipboard.writeText(url).then(() => {
      setCopiedWebhook(true);
      setTimeout(() => setCopiedWebhook(false), 2000);
    });
  };

  if (sessionLoading || configLoading) {
    return (
      <div className="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="text-gray-500 text-sm">Loading…</div>
      </div>
    );
  }

  if (configError) {
    return (
      <div className="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-sm text-red-700">{configError}</div>
      </div>
    );
  }

  const webhookUrl = config
    ? `${API_BASE_URL}/api/v1/webhook/${config.team_id}/${config.webhook_secret}`
    : '';

  const tabs: { id: Tab; label: string; adminOnly: boolean }[] = [
    { id: 'general', label: 'General', adminOnly: false },
    { id: 'datasources', label: 'Data Sources', adminOnly: true },
    { id: 'notifications', label: 'Notifications', adminOnly: true },
    { id: 'members', label: 'Members', adminOnly: false },
    { id: 'escalation', label: 'Escalation', adminOnly: true },
  ];

  const visibleTabs = tabs.filter((t) => !t.adminOnly || isAdmin);

  return (
    <div className="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <h1 className="text-3xl font-bold text-gray-900 mb-6">Team Settings</h1>

      {/* Tab navigation */}
      <div className="border-b border-gray-200 mb-6">
        <nav className="-mb-px flex gap-6">
          {visibleTabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => {
                setActiveTab(tab.id);
                setSaveError(null);
                setSaveSuccess(false);
              }}
              className={`py-3 px-1 text-sm font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? 'border-blue-600 text-blue-600'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* Save feedback */}
      {saveSuccess && (
        <div className="mb-4 bg-green-50 border border-green-200 rounded-lg p-3 text-sm text-green-700">
          Saved successfully.
        </div>
      )}
      {saveError && (
        <div className="mb-4 bg-red-50 border border-red-200 rounded-lg p-3 text-sm text-red-700">
          {saveError}
        </div>
      )}

      {/* ── General ── */}
      {activeTab === 'general' && (
        <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-4">
          <h2 className="text-lg font-semibold text-gray-900">Team Information</h2>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Team ID</label>
            <input
              type="text"
              value={config?.team_id ?? '—'}
              className="w-full px-3 py-2 border border-gray-300 rounded-md bg-gray-50 font-mono text-sm"
              readOnly
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Webhook URL</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={webhookUrl || 'Loading…'}
                className="flex-1 px-3 py-2 border border-gray-300 rounded-md bg-gray-50 font-mono text-xs"
                readOnly
              />
              <button
                onClick={copyWebhookUrl}
                className="px-4 py-2 bg-gray-600 text-white rounded-md hover:bg-gray-700 transition-colors text-sm"
              >
                {copiedWebhook ? 'Copied!' : 'Copy'}
              </button>
            </div>
            <p className="text-xs text-gray-500 mt-1">
              Paste this URL into your Grafana / Datadog / Prometheus alert contact point.
            </p>
          </div>
        </div>
      )}

      {/* ── Data Sources ── */}
      {activeTab === 'datasources' && isAdmin && (
        <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-4">
          <h2 className="text-lg font-semibold text-gray-900">Data Sources</h2>
          <p className="text-sm text-gray-500">
            These credentials are used by the worker to collect context when an alert fires.
          </p>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">GitHub Token</label>
            <input
              type="password"
              value={githubToken}
              onChange={(e) => setGithubToken(e.target.value)}
              placeholder={
                config?.github_token_set
                  ? '••••••••  (already saved — enter new to replace)'
                  : 'ghp_...'
              }
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
            <p className="text-xs text-gray-500 mt-1">Read-only token with contents:read scope.</p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">GitHub Repositories</label>
            <textarea
              value={githubRepos}
              onChange={(e) => setGithubRepos(e.target.value)}
              placeholder={'org/repo1\norg/repo2'}
              rows={3}
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm font-mono"
            />
            <p className="text-xs text-gray-500 mt-1">One repo per line (owner/name).</p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Prometheus Endpoint</label>
            <input
              type="text"
              value={prometheusEndpoint}
              onChange={(e) => setPrometheusEndpoint(e.target.value)}
              placeholder="http://prometheus:9090"
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Loki Endpoint</label>
            <input
              type="text"
              value={lokiEndpoint}
              onChange={(e) => setLokiEndpoint(e.target.value)}
              placeholder="http://loki:3100"
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">AI Backend</label>
              <select
                value={aiBackend}
                onChange={(e) => setAiBackend(e.target.value)}
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm bg-white"
              >
                <option value="ollama">Ollama (local)</option>
                <option value="claude">Claude (Anthropic)</option>
                <option value="openai">OpenAI</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Model</label>
              <input
                type="text"
                value={aiModel}
                onChange={(e) => setAiModel(e.target.value)}
                placeholder={
                  aiBackend === 'ollama'
                    ? 'llama3.2'
                    : aiBackend === 'claude'
                    ? 'claude-sonnet-4-6'
                    : 'gpt-4o'
                }
                className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
              />
            </div>
          </div>

          <button
            onClick={saveDataSources}
            disabled={saving}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors disabled:opacity-50 text-sm"
          >
            {saving ? 'Saving…' : 'Save Data Sources'}
          </button>
        </div>
      )}

      {/* ── Notifications ── */}
      {activeTab === 'notifications' && isAdmin && (
        <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-4">
          <h2 className="text-lg font-semibold text-gray-900">Notification Channels</h2>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Slack Webhook URL</label>
            <input
              type="text"
              value={slackWebhookUrl}
              onChange={(e) => setSlackWebhookUrl(e.target.value)}
              placeholder="https://hooks.slack.com/services/..."
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Slack Channel</label>
            <input
              type="text"
              value={slackChannel}
              onChange={(e) => setSlackChannel(e.target.value)}
              placeholder="#alerts"
              className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
            />
          </div>

          <button
            onClick={saveNotifications}
            disabled={saving}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors disabled:opacity-50 text-sm"
          >
            {saving ? 'Saving…' : 'Save Notifications'}
          </button>
        </div>
      )}

      {/* ── Members ── */}
      {activeTab === 'members' && (
        <div className="bg-white rounded-lg border border-gray-200">
          <div className="p-6 border-b border-gray-100">
            <h2 className="text-lg font-semibold text-gray-900">Team Members</h2>
            {isAdmin && (
              <p className="text-xs text-gray-500 mt-1">
                To add / remove members or change roles, use the{' '}
                <a href="/admin" className="text-blue-600 hover:underline">
                  Admin panel
                </a>
                .
              </p>
            )}
          </div>

          {membersLoading ? (
            <div className="p-6 text-sm text-gray-500">Loading members…</div>
          ) : members.length === 0 ? (
            <div className="p-6 text-sm text-gray-500">No members found.</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100">
                    <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                      Name
                    </th>
                    <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                      Email
                    </th>
                    <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                      Role
                    </th>
                    <th className="text-left px-6 py-3 text-xs font-medium text-gray-500 uppercase tracking-wider">
                      Phone
                    </th>
                    {isAdmin && <th className="px-6 py-3" />}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-50">
                  {members.map((member) => (
                    <tr key={member.id} className="hover:bg-gray-50">
                      <td className="px-6 py-3 font-medium text-gray-900">{member.name}</td>
                      <td className="px-6 py-3 text-gray-600">{member.email}</td>
                      <td className="px-6 py-3">
                        <span
                          className={`inline-flex px-2 py-0.5 rounded-full text-xs font-medium ${
                            member.role === 'admin'
                              ? 'bg-purple-100 text-purple-700'
                              : member.role === 'responder'
                              ? 'bg-blue-100 text-blue-700'
                              : 'bg-gray-100 text-gray-600'
                          }`}
                        >
                          {member.role}
                        </span>
                      </td>
                      <td className="px-6 py-3 text-gray-600">
                        {editingPhoneId === member.id ? (
                          <input
                            type="tel"
                            value={editingPhone}
                            onChange={(e) => setEditingPhone(e.target.value)}
                            placeholder="+1 555 000 0000"
                            className="px-2 py-1 border border-gray-300 rounded text-sm w-40 focus:outline-none focus:ring-1 focus:ring-blue-500"
                            autoFocus
                          />
                        ) : member.phone ? (
                          member.phone
                        ) : (
                          <span className="text-gray-400">—</span>
                        )}
                      </td>
                      {isAdmin && (
                        <td className="px-6 py-3 text-right">
                          {editingPhoneId === member.id ? (
                            <div className="flex justify-end gap-2">
                              <button
                                onClick={() => savePhone(member)}
                                disabled={saving}
                                className="text-xs px-2 py-1 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
                              >
                                Save
                              </button>
                              <button
                                onClick={() => setEditingPhoneId(null)}
                                className="text-xs px-2 py-1 border border-gray-300 rounded text-gray-600 hover:bg-gray-50"
                              >
                                Cancel
                              </button>
                            </div>
                          ) : (
                            <button
                              onClick={() => {
                                setEditingPhoneId(member.id);
                                setEditingPhone(member.phone ?? '');
                              }}
                              className="text-xs text-blue-600 hover:underline"
                            >
                              Edit phone
                            </button>
                          )}
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* ── Escalation ── */}
      {activeTab === 'escalation' && isAdmin && (
        <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-4">
          <h2 className="text-lg font-semibold text-gray-900">Escalation Policy</h2>
          <p className="text-sm text-gray-500">
            Controls how long to wait before escalating an unacknowledged alert to the next on-call
            layer.
          </p>

          {escalationLoading ? (
            <div className="text-sm text-gray-500">Loading…</div>
          ) : (
            <>
              <div className="grid grid-cols-3 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Escalation timeout{' '}
                    <span className="text-gray-400 font-normal">(minutes)</span>
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={120}
                    value={escalation.escalation_timeout_minutes}
                    onChange={(e) =>
                      setEscalation((prev) => ({
                        ...prev,
                        escalation_timeout_minutes: Number(e.target.value),
                      }))
                    }
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                  <p className="text-xs text-gray-500 mt-1">
                    Wait this long before paging the next layer.
                  </p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Repeat interval{' '}
                    <span className="text-gray-400 font-normal">(minutes)</span>
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={1440}
                    value={escalation.repeat_interval_minutes}
                    onChange={(e) =>
                      setEscalation((prev) => ({
                        ...prev,
                        repeat_interval_minutes: Number(e.target.value),
                      }))
                    }
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                  <p className="text-xs text-gray-500 mt-1">
                    Restart the chain after this interval if still unacked.
                  </p>
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">
                    Max repeats
                  </label>
                  <input
                    type="number"
                    min={0}
                    max={10}
                    value={escalation.max_repeats}
                    onChange={(e) =>
                      setEscalation((prev) => ({
                        ...prev,
                        max_repeats: Number(e.target.value),
                      }))
                    }
                    className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 text-sm"
                  />
                  <p className="text-xs text-gray-500 mt-1">
                    Stop after this many full chain repeats.
                  </p>
                </div>
              </div>

              <button
                onClick={saveEscalation}
                disabled={saving}
                className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors disabled:opacity-50 text-sm"
              >
                {saving ? 'Saving…' : 'Save Escalation Policy'}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
