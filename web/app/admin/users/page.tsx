'use client';

import { useEffect, useState, useCallback } from 'react';
import { api } from '@/lib/api';

interface LocalUser {
  id: string;
  username: string;
  email: string;
  name: string;
  is_superadmin: boolean;
  is_active: boolean;
  force_password_change: boolean;
  last_login_at?: string;
}

function generatePassword(): string {
  const upper = 'ABCDEFGHJKLMNPQRSTUVWXYZ';
  const lower = 'abcdefghjkmnpqrstuvwxyz';
  const digits = '23456789';
  const special = '!@#$%^&*';
  const all = upper + lower + digits + special;
  const arr = [
    upper[Math.floor(Math.random() * upper.length)],
    lower[Math.floor(Math.random() * lower.length)],
    digits[Math.floor(Math.random() * digits.length)],
    special[Math.floor(Math.random() * special.length)],
    ...Array.from({ length: 8 }, () => all[Math.floor(Math.random() * all.length)]),
  ];
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr.join('');
}

export default function AdminUsersPage() {
  const [users, setUsers] = useState<LocalUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState({ username: '', email: '', name: '', is_superadmin: false });
  const [formError, setFormError] = useState('');
  const [createdPassword, setCreatedPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    try {
      const data = await api.admin.users.list() as unknown as { users: LocalUser[]; count: number };
      setUsers(data.users ?? []);
    } catch {
      setError('Failed to load users');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError('');
    const password = generatePassword();
    try {
      await api.admin.users.create({ ...form, password });
      setCreating(false);
      setForm({ username: '', email: '', name: '', is_superadmin: false });
      setCreatedPassword(password);
      setShowPassword(false);
      setCopied(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to create user';
      try { setFormError(JSON.parse(msg).error ?? msg); } catch { setFormError(msg); }
    }
  }

  async function handleToggleActive(user: LocalUser) {
    await api.admin.users.update(user.id, { is_active: !user.is_active });
    load();
  }

  async function handleDelete(id: string) {
    if (!confirm('Delete this user? This cannot be undone.')) return;
    await api.admin.users.delete(id);
    load();
  }

  function handleCopy() {
    navigator.clipboard.writeText(createdPassword);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  if (loading) return <div className="text-gray-500">Loading…</div>;
  if (error) return <div className="text-red-600">{error}</div>;

  return (
    <div className="max-w-4xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-gray-900">Users</h1>
        <button
          onClick={() => { setCreating(!creating); setCreatedPassword(''); }}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors text-sm font-medium"
        >
          {creating ? 'Cancel' : '+ New User'}
        </button>
      </div>

      {createdPassword && (
        <div className="mb-6 p-4 bg-amber-50 border border-amber-200 rounded-lg">
          <div className="flex items-start justify-between gap-4">
            <div className="flex-1">
              <p className="text-sm font-semibold text-amber-800 mb-1">User created — save this password now</p>
              <p className="text-xs text-amber-700 mb-2">This password will not be shown again. The user must change it on first login.</p>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-1.5 bg-white border border-amber-200 rounded text-sm font-mono text-gray-900 tracking-wider">
                  {showPassword ? createdPassword : '•'.repeat(createdPassword.length)}
                </code>
                <button
                  onClick={() => setShowPassword(!showPassword)}
                  className="p-1.5 text-amber-700 hover:text-amber-900"
                  title={showPassword ? 'Hide password' : 'Show password'}
                >
                  {showPassword ? (
                    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" />
                    </svg>
                  ) : (
                    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                    </svg>
                  )}
                </button>
                <button
                  onClick={handleCopy}
                  className="px-3 py-1.5 bg-amber-100 hover:bg-amber-200 text-amber-800 text-xs font-medium rounded border border-amber-200"
                >
                  {copied ? 'Copied!' : 'Copy'}
                </button>
              </div>
            </div>
            <button onClick={() => setCreatedPassword('')} className="text-amber-500 hover:text-amber-700 text-lg leading-none">×</button>
          </div>
        </div>
      )}

      {creating && (
        <form onSubmit={handleCreate} className="bg-white border border-gray-200 rounded-lg p-6 mb-6 space-y-4">
          <h2 className="font-semibold text-gray-800">Create User</h2>
          <p className="text-xs text-gray-500">A secure password will be generated automatically and shown once after creation.</p>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Username</label>
              <input value={form.username} onChange={e => setForm({ ...form, username: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Email</label>
              <input type="email" value={form.email} onChange={e => setForm({ ...form, email: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" />
            </div>
            <div className="col-span-2">
              <label className="block text-xs font-medium text-gray-600 mb-1">Display Name</label>
              <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-gray-700">
            <input type="checkbox" checked={form.is_superadmin} onChange={e => setForm({ ...form, is_superadmin: e.target.checked })} />
            Superadmin
          </label>
          {formError && <p className="text-sm text-red-600">{formError}</p>}
          <button type="submit" className="px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">Create</button>
        </form>
      )}

      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Username</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Email</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Role</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Status</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {users.map(u => (
              <tr key={u.id} className="hover:bg-gray-50">
                <td className="px-4 py-3 font-mono text-gray-900">{u.username}</td>
                <td className="px-4 py-3 text-gray-600">{u.email}</td>
                <td className="px-4 py-3">
                  {u.is_superadmin
                    ? <span className="px-2 py-0.5 bg-purple-100 text-purple-700 rounded text-xs font-medium">Superadmin</span>
                    : <span className="px-2 py-0.5 bg-gray-100 text-gray-600 rounded text-xs">User</span>
                  }
                </td>
                <td className="px-4 py-3">
                  {u.is_active
                    ? <span className="px-2 py-0.5 bg-green-100 text-green-700 rounded text-xs font-medium">Active</span>
                    : <span className="px-2 py-0.5 bg-red-100 text-red-700 rounded text-xs font-medium">Inactive</span>
                  }
                </td>
                <td className="px-4 py-3 flex gap-2">
                  <button onClick={() => handleToggleActive(u)}
                    className="text-xs text-blue-600 hover:underline">
                    {u.is_active ? 'Deactivate' : 'Activate'}
                  </button>
                  <button onClick={() => handleDelete(u.id)}
                    className="text-xs text-red-600 hover:underline">
                    Delete
                  </button>
                </td>
              </tr>
            ))}
            {users.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">No users</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
