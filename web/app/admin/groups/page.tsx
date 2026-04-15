'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { api } from '@/lib/api';

interface LocalGroup {
  id: string;
  name: string;
  description: string;
  created_at: string;
}

export default function AdminGroupsPage() {
  const [groups, setGroups] = useState<LocalGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ name: '', description: '' });
  const [formError, setFormError] = useState('');

  const load = useCallback(async () => {
    const data = await api.admin.groups.list() as unknown as { groups: LocalGroup[]; count: number };
    setGroups(data.groups ?? []);
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError('');
    try {
      await api.admin.groups.create(form.name, form.description);
      setCreating(false);
      setForm({ name: '', description: '' });
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setFormError(JSON.parse(msg).error ?? msg); } catch { setFormError(msg); }
    }
  }

  async function handleDelete(id: string, name: string) {
    if (!confirm(`Delete group "${name}"?`)) return;
    await api.admin.groups.delete(id);
    load();
  }

  if (loading) return <div className="text-gray-500">Loading…</div>;

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-gray-900">Groups</h1>
        <button onClick={() => setCreating(!creating)}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium">
          {creating ? 'Cancel' : '+ New Group'}
        </button>
      </div>

      {creating && (
        <form onSubmit={handleCreate} className="bg-white border border-gray-200 rounded-lg p-6 mb-6 space-y-4">
          <h2 className="font-semibold text-gray-800">Create Group</h2>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Name</label>
            <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
              required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" placeholder="ops-team" />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Description</label>
            <input value={form.description} onChange={e => setForm({ ...form, description: e.target.value })}
              className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" placeholder="Optional" />
          </div>
          {formError && <p className="text-sm text-red-600">{formError}</p>}
          <button type="submit" className="px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">Create</button>
        </form>
      )}

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Name</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Description</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {groups.map(g => (
              <tr key={g.id} className="hover:bg-gray-50">
                <td className="px-4 py-3 font-medium text-gray-900">{g.name}</td>
                <td className="px-4 py-3 text-gray-500">{g.description || '—'}</td>
                <td className="px-4 py-3">
                  <div className="flex gap-3">
                    <Link href={`/admin/groups/${g.id}`}
                      className="text-xs text-blue-600 hover:underline">Manage</Link>
                    <button onClick={() => handleDelete(g.id, g.name)}
                      className="text-xs text-red-600 hover:underline">Delete</button>
                  </div>
                </td>
              </tr>
            ))}
            {groups.length === 0 && (
              <tr><td colSpan={3} className="px-4 py-8 text-center text-gray-400">No groups</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
