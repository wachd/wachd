'use client';

import { useEffect, useState, useCallback } from 'react';
import { api } from '@/lib/api';

interface Token {
  id: string;
  name: string;
  last_used_at?: string;
  expires_at?: string;
  created_at: string;
}

function formatDate(iso?: string) {
  if (!iso) return '—';
  return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
}

function formatRelative(iso?: string) {
  if (!iso) return 'never';
  const d = new Date(iso);
  const diff = Date.now() - d.getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export default function AdminTokensPage() {
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [formError, setFormError] = useState('');
  const [newToken, setNewToken] = useState('');
  const [showToken, setShowToken] = useState(false);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    try {
      const data = await api.admin.tokens.list();
      setTokens(data.tokens ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError('');
    try {
      const res = await api.admin.tokens.create(name);
      setNewToken(res.token);
      setShowToken(false);
      setCopied(false);
      setCreating(false);
      setName('');
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setFormError(JSON.parse(msg).error ?? msg); } catch { setFormError(msg); }
    }
  }

  async function handleRevoke(id: string, tokenName: string) {
    if (!confirm(`Revoke token "${tokenName}"? Any scripts using it will stop working immediately.`)) return;
    await api.admin.tokens.delete(id);
    load();
  }

  function handleCopy() {
    navigator.clipboard.writeText(newToken);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }

  if (loading) return <div className="text-gray-500">Loading…</div>;

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">API Tokens</h1>
          <p className="text-sm text-gray-500 mt-1">Personal access tokens for programmatic API access.</p>
        </div>
        <button
          onClick={() => { setCreating(!creating); setNewToken(''); }}
          className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 text-sm font-medium"
        >
          {creating ? 'Cancel' : 'Generate Token'}
        </button>
      </div>

      {/* Token revealed banner */}
      {newToken && (
        <div className="mb-6 p-4 bg-amber-50 border border-amber-200 rounded-lg">
          <div className="flex items-start justify-between gap-4">
            <div className="flex-1">
              <p className="text-sm font-semibold text-amber-800 mb-1">Token created — copy it now</p>
              <p className="text-xs text-amber-700 mb-2">This token will not be shown again. Store it in a secret manager or environment variable.</p>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-1.5 bg-white border border-amber-200 rounded text-sm font-mono text-gray-900 break-all">
                  {showToken ? newToken : '•'.repeat(Math.min(newToken.length, 50))}
                </code>
                <button
                  onClick={() => setShowToken(!showToken)}
                  className="p-1.5 text-amber-700 hover:text-amber-900 flex-shrink-0"
                  title={showToken ? 'Hide' : 'Show'}
                >
                  {showToken ? (
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
                  className="px-3 py-1.5 bg-amber-100 hover:bg-amber-200 text-amber-800 text-xs font-medium rounded border border-amber-200 flex-shrink-0"
                >
                  {copied ? 'Copied!' : 'Copy'}
                </button>
              </div>
              <p className="text-xs text-amber-600 mt-2 font-mono">
                Use as: <span className="font-semibold">Authorization: Bearer {showToken ? newToken : newToken.slice(0, 12) + '…'}</span>
              </p>
            </div>
            <button onClick={() => setNewToken('')} className="text-amber-500 hover:text-amber-700 text-lg leading-none flex-shrink-0">×</button>
          </div>
        </div>
      )}

      {/* Create form */}
      {creating && (
        <form onSubmit={handleCreate} className="bg-white border border-gray-200 rounded-lg p-5 mb-6 flex gap-3 items-end">
          <div className="flex-1">
            <label className="block text-xs font-medium text-gray-600 mb-1">Token name</label>
            <input
              value={name}
              onChange={e => setName(e.target.value)}
              required
              placeholder="e.g. ci-pipeline, terraform, monitoring-bot"
              className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm"
            />
            {formError && <p className="text-xs text-red-600 mt-1">{formError}</p>}
          </div>
          <button type="submit" className="px-4 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700">
            Generate
          </button>
        </form>
      )}

      {/* Usage hint */}
      <div className="mb-5 p-4 bg-gray-50 border border-gray-200 rounded-lg text-xs text-gray-600 font-mono">
        <p className="font-sans font-semibold text-gray-700 text-xs mb-2">Usage example</p>
        <p>curl -H <span className="text-blue-700">&quot;Authorization: Bearer wachd_…&quot;</span> \</p>
        <p className="pl-4">https://wachd.example.com/api/v1/admin/users</p>
      </div>

      {/* Tokens table */}
      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Name</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Created</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Last used</th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">Expires</th>
              <th className="px-4 py-3" />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {tokens.map(t => (
              <tr key={t.id} className="hover:bg-gray-50">
                <td className="px-4 py-3 font-medium text-gray-900">{t.name}</td>
                <td className="px-4 py-3 text-gray-500 text-xs">{formatDate(t.created_at)}</td>
                <td className="px-4 py-3 text-gray-500 text-xs">{formatRelative(t.last_used_at)}</td>
                <td className="px-4 py-3 text-gray-500 text-xs">
                  {t.expires_at
                    ? <span className={new Date(t.expires_at) < new Date() ? 'text-red-600' : ''}>{formatDate(t.expires_at)}</span>
                    : <span className="text-gray-400">Never</span>
                  }
                </td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => handleRevoke(t.id, t.name)}
                    className="text-xs text-red-600 hover:underline"
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
            {tokens.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-gray-400">
                  No API tokens yet. Generate one to use with curl, Terraform, or CI pipelines.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
