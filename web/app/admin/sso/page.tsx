'use client';

import { useEffect, useState, useCallback } from 'react';
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

export default function AdminSSOPage() {
  const [providers, setProviders] = useState<SSOProvider[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [testResults, setTestResults] = useState<Record<string, string>>({});
  const [form, setForm] = useState({
    name: '', issuer_url: '', client_id: '', client_secret: '',
    scopes: 'openid profile email', enabled: true, auto_provision: true,
  });
  const [formError, setFormError] = useState('');

  const load = useCallback(async () => {
    const data = await api.admin.sso.list() as unknown as { providers: SSOProvider[]; count: number };
    setProviders(data.providers ?? []);
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError('');
    try {
      await api.admin.sso.create({
        name: form.name,
        issuer_url: form.issuer_url,
        client_id: form.client_id,
        client_secret: form.client_secret,
        scopes: form.scopes.split(/\s+/).filter(Boolean),
        enabled: form.enabled,
        auto_provision: form.auto_provision,
      });
      setCreating(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setFormError(JSON.parse(msg).error ?? msg); } catch { setFormError(msg); }
    }
  }

  async function handleTest(id: string) {
    setTestResults(r => ({ ...r, [id]: 'testing…' }));
    try {
      await api.admin.sso.test(id);
      setTestResults(r => ({ ...r, [id]: 'ok' }));
    } catch {
      setTestResults(r => ({ ...r, [id]: 'failed' }));
    }
  }

  async function handleDelete(id: string, name: string) {
    if (!confirm(`Delete SSO provider "${name}"?`)) return;
    await api.admin.sso.delete(id);
    load();
  }

  if (loading) return <div className="text-gray-500">Loading…</div>;

  return (
    <div className="max-w-4xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-gray-900">SSO Providers</h1>
        <button onClick={() => setCreating(!creating)}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium">
          {creating ? 'Cancel' : '+ Add Provider'}
        </button>
      </div>

      {creating && (
        <form onSubmit={handleCreate} className="bg-white border border-gray-200 rounded-lg p-6 mb-6 space-y-4">
          <h2 className="font-semibold text-gray-800">Add OIDC Provider</h2>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Display Name</label>
              <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" placeholder="Corporate Entra" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Issuer URL</label>
              <input value={form.issuer_url} onChange={e => setForm({ ...form, issuer_url: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
                placeholder="https://login.microsoftonline.com/{tenant}/v2.0" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Client ID</label>
              <input value={form.client_id} onChange={e => setForm({ ...form, client_id: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm font-mono" />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Client Secret</label>
              <input type="password" value={form.client_secret} onChange={e => setForm({ ...form, client_secret: e.target.value })}
                required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm" />
            </div>
            <div className="col-span-2">
              <label className="block text-xs font-medium text-gray-600 mb-1">Scopes (space-separated)</label>
              <input value={form.scopes} onChange={e => setForm({ ...form, scopes: e.target.value })}
                className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm font-mono" />
            </div>
          </div>
          <div className="flex gap-6">
            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input type="checkbox" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} />
              Enabled
            </label>
            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input type="checkbox" checked={form.auto_provision} onChange={e => setForm({ ...form, auto_provision: e.target.checked })} />
              Auto-provision users
            </label>
          </div>
          {formError && <p className="text-sm text-red-600">{formError}</p>}
          <button type="submit" className="px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700">Save Provider</button>
        </form>
      )}

      <div className="space-y-3">
        {providers.map(p => (
          <div key={p.id} className="bg-white border border-gray-200 rounded-lg p-5">
            <div className="flex items-start justify-between">
              <div>
                <div className="flex items-center gap-2 mb-1">
                  <span className="font-semibold text-gray-900">{p.name}</span>
                  {p.enabled
                    ? <span className="px-2 py-0.5 bg-green-100 text-green-700 rounded text-xs">Enabled</span>
                    : <span className="px-2 py-0.5 bg-gray-100 text-gray-500 rounded text-xs">Disabled</span>
                  }
                </div>
                <p className="text-xs text-gray-500 font-mono">{p.issuer_url}</p>
                <p className="text-xs text-gray-500 mt-0.5">Client ID: <span className="font-mono">{p.client_id}</span></p>
              </div>
              <div className="flex gap-2">
                <Link href={`/admin/sso/${p.id}`}
                  className="text-xs px-3 py-1.5 bg-blue-600 text-white rounded hover:bg-blue-700">
                  Manage
                </Link>
                <button onClick={() => handleTest(p.id)}
                  className="text-xs px-3 py-1.5 border border-gray-300 rounded hover:bg-gray-50">
                  {testResults[p.id] === 'testing…' ? 'Testing…' :
                    testResults[p.id] === 'ok' ? '✓ OK' :
                    testResults[p.id] === 'failed' ? '✗ Failed' : 'Test'}
                </button>
                <button onClick={() => handleDelete(p.id, p.name)}
                  className="text-xs px-3 py-1.5 text-red-600 border border-red-200 rounded hover:bg-red-50">
                  Delete
                </button>
              </div>
            </div>
          </div>
        ))}
        {providers.length === 0 && (
          <div className="bg-white border border-gray-200 rounded-lg p-8 text-center text-gray-400">
            No SSO providers configured. Add one above to enable SSO login.
          </div>
        )}
      </div>
    </div>
  );
}
