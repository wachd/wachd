'use client';

import { useEffect, useState, useCallback } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { api } from '@/lib/api';

interface SSOProvider {
  id: string;
  name: string;
  provider_type: string;
  issuer_url: string;
  client_id: string;
  client_secret_set: boolean;
  scopes: string[];
  enabled: boolean;
  auto_provision: boolean;
}

export default function SSOProviderDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [provider, setProvider] = useState<SSOProvider | null>(null);
  const [loading, setLoading] = useState(true);
  const [testResult, setTestResult] = useState('');
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState({ name: '', client_secret: '', enabled: true, auto_provision: true });
  const [saveError, setSaveError] = useState('');

  const load = useCallback(async () => {
    const data = await api.admin.sso.get(id) as unknown as SSOProvider;
    setProvider(data);
    setForm({ name: data.name, client_secret: '', enabled: data.enabled, auto_provision: data.auto_provision });
    setLoading(false);
  }, [id]);

  useEffect(() => { load(); }, [load]);

  async function handleTest() {
    setTestResult('testing…');
    try {
      await api.admin.sso.test(id);
      setTestResult('ok');
    } catch {
      setTestResult('failed');
    }
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaveError('');
    try {
      const update: Record<string, unknown> = {
        name: form.name,
        enabled: form.enabled,
        auto_provision: form.auto_provision,
      };
      if (form.client_secret) update.client_secret = form.client_secret;
      await api.admin.sso.update(id, update);
      setEditing(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setSaveError(JSON.parse(msg).error ?? msg); } catch { setSaveError(msg); }
    }
  }

  if (loading) return <div className="text-gray-500 p-4">Loading…</div>;
  if (!provider) return <div className="text-gray-500 p-4">Provider not found.</div>;

  return (
    <div className="max-w-2xl">
      <Link href="/admin/sso" className="text-sm text-blue-600 hover:underline mb-4 inline-block">
        ← Back to SSO Providers
      </Link>

      <div className="bg-white border border-gray-200 rounded-lg p-6">
        <div className="flex items-center justify-between mb-6">
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-bold text-gray-900">{provider.name}</h1>
            {provider.enabled
              ? <span className="px-2 py-0.5 bg-green-100 text-green-700 rounded text-xs">Enabled</span>
              : <span className="px-2 py-0.5 bg-gray-100 text-gray-500 rounded text-xs">Disabled</span>}
          </div>
          <div className="flex gap-2">
            <button onClick={handleTest}
              className="text-sm px-3 py-1.5 border border-gray-300 rounded hover:bg-gray-50">
              {testResult === 'testing…' ? 'Testing…' :
                testResult === 'ok' ? '✓ OK' :
                testResult === 'failed' ? '✗ Failed' : 'Test Connection'}
            </button>
            <button onClick={() => setEditing(!editing)}
              className="text-sm px-3 py-1.5 border border-gray-300 rounded hover:bg-gray-50">
              {editing ? 'Cancel' : 'Edit'}
            </button>
          </div>
        </div>

        {editing ? (
          <form onSubmit={handleSave} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Display Name</label>
              <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">
                New Client Secret <span className="text-gray-400">(leave blank to keep current)</span>
              </label>
              <input type="password" value={form.client_secret}
                onChange={e => setForm({ ...form, client_secret: e.target.value })}
                className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
                placeholder="••••••••" />
            </div>
            <div className="flex gap-6">
              <label className="flex items-center gap-2 text-sm text-gray-700">
                <input type="checkbox" checked={form.enabled}
                  onChange={e => setForm({ ...form, enabled: e.target.checked })} />
                Enabled
              </label>
              <label className="flex items-center gap-2 text-sm text-gray-700">
                <input type="checkbox" checked={form.auto_provision}
                  onChange={e => setForm({ ...form, auto_provision: e.target.checked })} />
                Auto-provision users
              </label>
            </div>
            {saveError && <p className="text-sm text-red-600">{saveError}</p>}
            <button type="submit" className="px-4 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700">
              Save Changes
            </button>
          </form>
        ) : (
          <dl className="grid grid-cols-2 gap-4 text-sm">
            <div>
              <dt className="text-gray-500">Issuer URL</dt>
              <dd className="font-mono text-xs text-gray-800 mt-0.5 break-all">{provider.issuer_url}</dd>
            </div>
            <div>
              <dt className="text-gray-500">Client ID</dt>
              <dd className="font-mono text-xs text-gray-800 mt-0.5">{provider.client_id}</dd>
            </div>
            <div>
              <dt className="text-gray-500">Client Secret</dt>
              <dd className="text-gray-800 mt-0.5">{provider.client_secret_set ? '••••••••  (set)' : 'Not set'}</dd>
            </div>
            <div>
              <dt className="text-gray-500">Scopes</dt>
              <dd className="font-mono text-xs text-gray-800 mt-0.5">{provider.scopes?.join(' ')}</dd>
            </div>
            <div>
              <dt className="text-gray-500">Auto-provision</dt>
              <dd className="mt-0.5">{provider.auto_provision ? 'Yes' : 'No'}</dd>
            </div>
          </dl>
        )}

        <div className="mt-6 pt-5 border-t border-gray-100">
          <p className="text-xs text-gray-400">
            To map AD groups to teams, go to{' '}
            <Link href="/admin/teams" className="text-blue-500 hover:underline">
              Admin → Teams
            </Link>{' '}
            and configure group mappings per team.
          </p>
        </div>
      </div>
    </div>
  );
}
