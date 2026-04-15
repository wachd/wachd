'use client';

import { useEffect, useState } from 'react';
import { api } from '@/lib/api';

interface PasswordPolicy {
  min_length: number;
  require_uppercase: boolean;
  require_lowercase: boolean;
  require_number: boolean;
  require_special: boolean;
  max_failed_attempts: number;
  lockout_duration_minutes: number;
  updated_at: string;
}

export default function PasswordPolicyPage() {
  const [policy, setPolicy] = useState<PasswordPolicy | null>(null);
  const [form, setForm] = useState<Omit<PasswordPolicy, 'updated_at'> | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    api.admin.passwordPolicy.get().then((data) => {
      const p = data as PasswordPolicy;
      setPolicy(p);
      setForm({
        min_length: p.min_length,
        require_uppercase: p.require_uppercase,
        require_lowercase: p.require_lowercase,
        require_number: p.require_number,
        require_special: p.require_special,
        max_failed_attempts: p.max_failed_attempts,
        lockout_duration_minutes: p.lockout_duration_minutes,
      });
      setLoading(false);
    });
  }, []);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setSaving(true);
    try {
      const updated = await api.admin.passwordPolicy.update(form as Record<string, unknown>) as PasswordPolicy;
      setPolicy(updated);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save';
      try { setError(JSON.parse(msg).error ?? msg); } catch { setError(msg); }
    } finally {
      setSaving(false);
    }
  }

  if (loading || !form) return <div className="text-gray-500">Loading…</div>;

  return (
    <div className="max-w-lg">
      <h1 className="text-2xl font-bold text-gray-900 mb-2">Password Policy</h1>
      <p className="text-gray-500 text-sm mb-6">
        Applied to all local user accounts. Changes take effect on next login or password change.
      </p>

      <form onSubmit={handleSave} className="bg-white border border-gray-200 rounded-lg p-6 space-y-5">
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Minimum Length
          </label>
          <input
            type="number" min={8} max={64}
            value={form.min_length}
            onChange={e => setForm({ ...form, min_length: parseInt(e.target.value) || 12 })}
            className="w-32 px-3 py-1.5 border border-gray-300 rounded text-sm"
          />
        </div>

        <div className="space-y-2">
          <p className="text-sm font-medium text-gray-700">Character Requirements</p>
          {([
            ['require_uppercase', 'Require uppercase letter'],
            ['require_lowercase', 'Require lowercase letter'],
            ['require_number', 'Require number'],
            ['require_special', 'Require special character'],
          ] as [keyof typeof form, string][]).map(([key, label]) => (
            <label key={key} className="flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={form[key] as boolean}
                onChange={e => setForm({ ...form, [key]: e.target.checked })}
              />
              {label}
            </label>
          ))}
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Max Failed Attempts
            </label>
            <input
              type="number" min={0} max={20}
              value={form.max_failed_attempts}
              onChange={e => setForm({ ...form, max_failed_attempts: parseInt(e.target.value) || 5 })}
              className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
            />
            <p className="text-xs text-gray-400 mt-1">0 = no lockout</p>
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Lockout Duration (minutes)
            </label>
            <input
              type="number" min={1} max={1440}
              value={form.lockout_duration_minutes}
              onChange={e => setForm({ ...form, lockout_duration_minutes: parseInt(e.target.value) || 30 })}
              className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
            />
          </div>
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        <div className="flex items-center gap-3">
          <button type="submit" disabled={saving}
            className="px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700 disabled:bg-blue-400">
            {saving ? 'Saving…' : 'Save Policy'}
          </button>
          {saved && <span className="text-sm text-green-600">Saved</span>}
        </div>

        {policy && (
          <p className="text-xs text-gray-400">
            Last updated: {new Date(policy.updated_at).toLocaleString()}
          </p>
        )}
      </form>
    </div>
  );
}
