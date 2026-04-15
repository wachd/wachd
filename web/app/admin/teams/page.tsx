'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { api } from '@/lib/api';

interface Team {
  id: string;
  name: string;
  webhook_secret: string;
  created_at: string;
}

export default function AdminTeamsPage() {
  const [teams, setTeams] = useState<Team[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    const data = await api.admin.teams.list() as unknown as { teams: Team[] };
    setTeams(data.teams ?? []);
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    try {
      await api.admin.teams.create(name);
      setName('');
      setCreating(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setError(JSON.parse(msg).error ?? msg); } catch { setError(msg); }
    }
  }

  if (loading) return <div className="text-gray-500">Loading…</div>;

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Teams</h1>
          <p className="text-sm text-gray-500 mt-1">Configure which AD groups have access to each team.</p>
        </div>
        <button onClick={() => setCreating(!creating)}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium">
          {creating ? 'Cancel' : '+ New Team'}
        </button>
      </div>

      {creating && (
        <form onSubmit={handleCreate} className="bg-white border border-gray-200 rounded-lg p-5 mb-4 flex gap-3 items-end">
          <div className="flex-1">
            <label className="block text-xs font-medium text-gray-600 mb-1">Team Name</label>
            <input
              value={name}
              onChange={e => setName(e.target.value)}
              required
              autoFocus
              placeholder="e.g. Payments Team"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm"
            />
            {error && <p className="text-xs text-red-600 mt-1">{error}</p>}
          </div>
          <button type="submit" className="px-4 py-2 bg-blue-600 text-white rounded-lg text-sm hover:bg-blue-700">
            Create
          </button>
        </form>
      )}

      <div className="space-y-3">
        {teams.map(t => (
          <div key={t.id} className="bg-white border border-gray-200 rounded-lg p-5 flex items-center justify-between">
            <div>
              <p className="font-semibold text-gray-900">{t.name}</p>
              <p className="text-xs text-gray-400 font-mono mt-0.5">{t.id}</p>
            </div>
            <div className="flex items-center gap-2">
              <Link
                href={`/admin/teams/${t.id}`}
                className="text-sm px-4 py-1.5 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
              >
                Manage Access
              </Link>
              <button
                onClick={async () => {
                  if (!confirm(`Delete team "${t.name}"? This removes all its incidents, schedules, and data.`)) return;
                  try {
                    await api.admin.teams.delete(t.id);
                    load();
                  } catch (err: unknown) {
                    alert(err instanceof Error ? err.message : 'Failed to delete team');
                  }
                }}
                className="text-sm px-3 py-1.5 text-red-600 border border-red-200 rounded-lg hover:bg-red-50"
              >
                Delete
              </button>
            </div>
          </div>
        ))}
        {teams.length === 0 && (
          <div className="bg-white border border-gray-200 rounded-lg p-8 text-center text-gray-400">
            No teams yet. Create one above.
          </div>
        )}
      </div>
    </div>
  );
}

