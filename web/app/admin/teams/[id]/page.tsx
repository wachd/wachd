'use client';

import { useEffect, useState, useCallback } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { api } from '@/lib/api';

interface Team {
  id: string;
  name: string;
  webhook_secret: string;
}

interface GroupMapping {
  id: string;
  provider: string;
  group_id: string;
  group_name?: string;
  team_id: string;
  role: string;
}

interface SSOProvider {
  id: string;
  name: string;
}

export default function TeamDetailPage() {
  const { id } = useParams<{ id: string }>();

  const [team, setTeam] = useState<Team | null>(null);
  const [mappings, setMappings] = useState<GroupMapping[]>([]);
  const [providers, setProviders] = useState<SSOProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState({ provider_id: '', group_id: '', group_name: '', role: 'viewer' });
  const [formError, setFormError] = useState('');

  const load = useCallback(async () => {
    const [teamsData, mappingsData, providersData] = await Promise.all([
      api.admin.teams.list() as unknown as { teams: Team[] },
      api.admin.groupMappings.list() as unknown as { mappings: GroupMapping[] },
      api.admin.sso.list() as unknown as { providers: SSOProvider[] },
    ]);
    const found = (teamsData.teams ?? []).find(t => t.id === id) ?? null;
    setTeam(found);
    // Only mappings for this team
    setMappings((mappingsData.mappings ?? []).filter(m => m.team_id === id));
    setProviders(providersData.providers ?? []);
    // Pre-select first provider
    if (providersData.providers?.length && !form.provider_id) {
      setForm(f => ({ ...f, provider_id: providersData.providers[0].id }));
    }
    setLoading(false);
  }, [id]);

  useEffect(() => { load(); }, [load]);

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault();
    setFormError('');
    if (!form.provider_id) {
      setFormError('Please select an SSO provider');
      return;
    }
    try {
      await api.admin.groupMappings.create({
        provider_id: form.provider_id,
        group_id: form.group_id,
        group_name: form.group_name || undefined,
        team_id: id,
        role: form.role,
      });
      setForm(f => ({ ...f, group_id: '', group_name: '' }));
      setAdding(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setFormError(JSON.parse(msg).error ?? msg); } catch { setFormError(msg); }
    }
  }

  async function handleDelete(mappingId: string) {
    if (!confirm('Remove this group mapping?')) return;
    await api.admin.groupMappings.delete(mappingId);
    load();
  }

  if (loading) return <div className="text-gray-500 p-4">Loading…</div>;
  if (!team) return <div className="text-gray-500 p-4">Team not found.</div>;

  return (
    <div className="max-w-3xl">
      <Link href="/admin/teams" className="text-sm text-blue-600 hover:underline mb-4 inline-block">
        ← Back to Teams
      </Link>

      {/* Team info */}
      <div className="bg-white border border-gray-200 rounded-lg p-6 mb-6">
        <h1 className="text-xl font-bold text-gray-900 mb-3">{team.name}</h1>
        <div className="text-sm text-gray-500">
          <span className="font-medium text-gray-700">Webhook secret: </span>
          <span className="font-mono">{team.webhook_secret}</span>
        </div>
      </div>

      {/* Group Mappings */}
      <div className="bg-white border border-gray-200 rounded-lg p-6">
        <div className="flex items-center justify-between mb-1">
          <h2 className="text-base font-semibold text-gray-900">Group Mappings</h2>
          <button
            onClick={() => setAdding(!adding)}
            className="text-sm px-3 py-1.5 bg-blue-600 text-white rounded hover:bg-blue-700"
          >
            {adding ? 'Cancel' : '+ Add Mapping'}
          </button>
        </div>
        <p className="text-xs text-gray-500 mb-4">
          Members of these AD groups will automatically get access to <strong>{team.name}</strong> on login.
        </p>

        {adding && (
          <form onSubmit={handleAdd} className="bg-gray-50 border border-gray-200 rounded-lg p-4 mb-4 space-y-3">
            {providers.length > 0 && (
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">SSO Provider</label>
                <select
                  value={form.provider_id}
                  onChange={e => setForm({ ...form, provider_id: e.target.value })}
                  required
                  className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm bg-white"
                >
                  <option value="">— select provider —</option>
                  {providers.map(p => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
              </div>
            )}
            <div className="grid grid-cols-2 gap-3">
              <div className="col-span-2">
                <label className="block text-xs font-medium text-gray-600 mb-1">
                  AD Group Object ID
                </label>
                <input
                  value={form.group_id}
                  onChange={e => setForm({ ...form, group_id: e.target.value })}
                  required
                  placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                  className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm font-mono"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">
                  Display Name <span className="text-gray-400">(optional)</span>
                </label>
                <input
                  value={form.group_name}
                  onChange={e => setForm({ ...form, group_name: e.target.value })}
                  placeholder="e.g. SRE Team"
                  className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Role</label>
                <select
                  value={form.role}
                  onChange={e => setForm({ ...form, role: e.target.value })}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm bg-white"
                >
                  <option value="viewer">Viewer — read-only</option>
                  <option value="responder">Responder — ack &amp; resolve</option>
                  <option value="admin">Admin — full team access</option>
                </select>
              </div>
            </div>
            {formError && <p className="text-xs text-red-600">{formError}</p>}
            <button type="submit" className="px-4 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700">
              Save
            </button>
          </form>
        )}

        {mappings.length === 0 ? (
          <p className="text-sm text-gray-400 text-center py-8">
            No group mappings yet.<br />
            <span className="text-xs">Add an AD group to grant its members access to this team.</span>
          </p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-gray-500 border-b border-gray-100">
                <th className="pb-2 font-medium">AD Group</th>
                <th className="pb-2 font-medium">Provider</th>
                <th className="pb-2 font-medium">Role</th>
                <th className="pb-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-50">
              {mappings.map(m => (
                <tr key={m.id}>
                  <td className="py-3 pr-4">
                    {m.group_name && (
                      <p className="font-medium text-gray-900">{m.group_name}</p>
                    )}
                    <p className="font-mono text-xs text-gray-500">{m.group_id}</p>
                  </td>
                  <td className="py-3 pr-4 text-gray-500 text-xs uppercase tracking-wide">{m.provider}</td>
                  <td className="py-3 pr-4">
                    <span className={`px-2 py-0.5 rounded text-xs font-medium ${
                      m.role === 'admin'     ? 'bg-red-100 text-red-700' :
                      m.role === 'responder' ? 'bg-blue-100 text-blue-700' :
                                              'bg-gray-100 text-gray-600'
                    }`}>
                      {m.role}
                    </span>
                  </td>
                  <td className="py-3 text-right">
                    <button
                      onClick={() => handleDelete(m.id)}
                      className="text-xs text-red-500 hover:text-red-700"
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
