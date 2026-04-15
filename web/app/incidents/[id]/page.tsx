'use client';

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { api } from '@/lib/api';
import { useSession } from '@/lib/session-context';
import type { Incident } from '@/lib/types';

function formatDate(dateString: string) {
  return new Date(dateString).toLocaleString();
}

export default function IncidentDetailPage() {
  const params = useParams();
  const router = useRouter();
  const { primaryTeamId } = useSession();
  const [incident, setIncident] = useState<Incident | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showSnoozeDialog, setShowSnoozeDialog] = useState(false);
  const [snoozeMinutes, setSnoozeMinutes] = useState(30);

  useEffect(() => {
    async function fetchIncident() {
      if (!primaryTeamId) return;
      try {
        const data = await api.incidents.get(primaryTeamId, params.id as string);
        setIncident(data);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch incident');
      } finally {
        setLoading(false);
      }
    }

    if (params.id && primaryTeamId) {
      fetchIncident();
    }
  }, [params.id]);

  const handleAcknowledge = async () => {
    if (!incident) return;
    try {
      await api.incidents.acknowledge(primaryTeamId!, incident.id);
      setIncident({ ...incident, status: 'acknowledged' });
    } catch (err) {
      alert('Failed to acknowledge incident');
    }
  };

  const handleResolve = async () => {
    if (!incident) return;
    try {
      await api.incidents.resolve(primaryTeamId!, incident.id);
      setIncident({ ...incident, status: 'resolved' });
    } catch (err) {
      alert('Failed to resolve incident');
    }
  };

  const handleReopen = async () => {
    if (!incident) return;
    try {
      await api.incidents.reopen(primaryTeamId!, incident.id);
      setIncident({ ...incident, status: 'open' });
    } catch (err) {
      alert('Failed to reopen incident');
    }
  };

  const handleSnooze = async () => {
    if (!incident) return;
    try {
      await api.incidents.snooze(primaryTeamId!, incident.id, snoozeMinutes);
      setIncident({ ...incident, status: 'snoozed' });
      setShowSnoozeDialog(false);
    } catch (err) {
      alert('Failed to snooze incident');
    }
  };

  if (loading) {
    return (
      <div className="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="animate-pulse space-y-6">
          <div className="h-8 bg-gray-200 rounded w-1/3"></div>
          <div className="h-64 bg-gray-200 rounded"></div>
        </div>
      </div>
    );
  }

  if (error || !incident) {
    return (
      <div className="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4">
          <h3 className="text-red-800 font-medium">Error loading incident</h3>
          <p className="text-red-600 text-sm mt-1">{error}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <button
        onClick={() => router.back()}
        className="mb-4 text-gray-600 hover:text-gray-900 flex items-center gap-2"
      >
        <svg
          className="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M15 19l-7-7 7-7"
          />
        </svg>
        Back to incidents
      </button>

      <div className="space-y-6">
        {/* Header */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="flex items-start justify-between mb-4">
            <h1 className="text-2xl font-bold text-gray-900">{incident.title}</h1>

            {/* Action Buttons */}
            <div className="flex gap-2">
              {incident.status === 'open' && (
                <button
                  onClick={handleAcknowledge}
                  className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors"
                >
                  Acknowledge
                </button>
              )}

              {incident.status === 'acknowledged' && (
                <button
                  onClick={handleResolve}
                  className="px-4 py-2 bg-green-600 text-white rounded-md hover:bg-green-700 transition-colors"
                >
                  Resolve
                </button>
              )}

              {incident.status === 'resolved' && (
                <button
                  onClick={handleReopen}
                  className="px-4 py-2 bg-orange-600 text-white rounded-md hover:bg-orange-700 transition-colors"
                >
                  Reopen
                </button>
              )}

              {(incident.status === 'open' || incident.status === 'acknowledged') && (
                <button
                  onClick={() => setShowSnoozeDialog(true)}
                  className="px-4 py-2 bg-gray-600 text-white rounded-md hover:bg-gray-700 transition-colors"
                >
                  Snooze
                </button>
              )}
            </div>
          </div>

          <div className="flex flex-wrap gap-3 mb-4">
            <span className="inline-flex items-center px-3 py-1 rounded-full text-sm font-medium bg-red-100 text-red-800">
              {incident.severity}
            </span>
            <span
              className={`inline-flex items-center px-3 py-1 rounded-full text-sm font-medium ${
                incident.status === 'open'
                  ? 'bg-yellow-100 text-yellow-800'
                  : incident.status === 'acknowledged'
                  ? 'bg-blue-100 text-blue-800'
                  : incident.status === 'resolved'
                  ? 'bg-green-100 text-green-800'
                  : 'bg-purple-100 text-purple-800'
              }`}
            >
              {incident.status}
            </span>
            <span className="text-sm text-gray-600">
              Source: {incident.source}
            </span>
            <span className="text-sm text-gray-600">
              Fired: {formatDate(incident.fired_at)}
            </span>
            {incident.snoozed_until && (
              <span className="text-sm text-purple-600 font-medium">
                Snoozed until: {formatDate(incident.snoozed_until)}
              </span>
            )}
          </div>

          {incident.message && (
            <p className="text-gray-700 mb-4">{incident.message}</p>
          )}
        </div>

        {/* AI Analysis */}
        {incident.analysis?.root_cause && (
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <h2 className="text-lg font-semibold text-gray-900 mb-4 flex items-center gap-2">
              <span>🤖</span>
              Root Cause Analysis
            </h2>
            <div className="space-y-4">
              <div>
                <h3 className="text-sm font-medium text-gray-700 mb-1">
                  Probable Cause
                </h3>
                <p className="text-gray-900">{incident.analysis.root_cause}</p>
              </div>
              {incident.analysis.suggested_action && (
                <div>
                  <h3 className="text-sm font-medium text-gray-700 mb-1">
                    Suggested Action
                  </h3>
                  <p className="text-gray-900">
                    {incident.analysis.suggested_action}
                  </p>
                </div>
              )}
              {incident.analysis.confidence && (
                <div>
                  <h3 className="text-sm font-medium text-gray-700 mb-1">
                    Confidence
                  </h3>
                  <span
                    className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                      incident.analysis.confidence === 'high'
                        ? 'bg-green-100 text-green-800'
                        : incident.analysis.confidence === 'medium'
                        ? 'bg-yellow-100 text-yellow-800'
                        : 'bg-gray-100 text-gray-800'
                    }`}
                  >
                    {incident.analysis.confidence}
                  </span>
                </div>
              )}
              {incident.analysis.correlations &&
                incident.analysis.correlations.length > 0 && (
                  <div>
                    <h3 className="text-sm font-medium text-gray-700 mb-2">
                      Timeline Correlations
                    </h3>
                    <ul className="list-disc list-inside space-y-1 text-gray-900">
                      {incident.analysis.correlations.map((corr, idx) => (
                        <li key={idx} className="text-sm">
                          {corr}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
            </div>
          </div>
        )}

        {/* Context */}
        {incident.context && (
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <h2 className="text-lg font-semibold text-gray-900 mb-4">
              Collected Context
            </h2>

            {/* Commits */}
            {incident.context.commits && incident.context.commits.length > 0 && (
              <div className="mb-6">
                <h3 className="text-sm font-medium text-gray-700 mb-2">
                  Recent Commits ({incident.context.commits.length})
                </h3>
                <div className="space-y-2">
                  {incident.context.commits.map((commit, idx) => (
                    <div
                      key={idx}
                      className="bg-gray-50 rounded p-3 text-sm font-mono"
                    >
                      <div className="flex items-center gap-2 mb-1">
                        <span className="text-gray-500">{commit.sha.slice(0, 7)}</span>
                        <span className="text-gray-700">{commit.author}</span>
                        <span className="text-gray-400">
                          {formatDate(commit.timestamp)}
                        </span>
                      </div>
                      <p className="text-gray-900">{commit.message}</p>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Logs */}
            {incident.context.logs && incident.context.logs.length > 0 && (
              <div className="mb-6">
                <h3 className="text-sm font-medium text-gray-700 mb-2">
                  Error Logs ({incident.context.logs.length})
                </h3>
                <div className="bg-gray-900 rounded p-4 overflow-x-auto max-h-96 overflow-y-auto">
                  {incident.context.logs.map((log, idx) => (
                    <div key={idx} className="text-sm font-mono text-gray-300 mb-1">
                      <span className="text-gray-500">
                        {formatDate(log.timestamp)}
                      </span>{' '}
                      <span className="text-red-400">[{log.level}]</span>{' '}
                      <span>{log.message}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Metrics */}
            {incident.context.metrics && incident.context.metrics.length > 0 && (
              <div>
                <h3 className="text-sm font-medium text-gray-700 mb-2">
                  Metrics ({incident.context.metrics.length} points)
                </h3>
                <div className="bg-gray-50 rounded p-4">
                  <p className="text-sm text-gray-600">
                    Metric data available ({incident.context.metrics.length}{' '}
                    data points)
                  </p>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Alert Payload */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h2 className="text-lg font-semibold text-gray-900 mb-4">
            Alert Payload
          </h2>
          <pre className="bg-gray-50 rounded p-4 overflow-x-auto text-sm">
            {JSON.stringify(incident.alert_payload, null, 2)}
          </pre>
        </div>
      </div>

      {/* Snooze Dialog */}
      {showSnoozeDialog && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white rounded-lg p-6 max-w-md w-full mx-4">
            <h3 className="text-lg font-semibold text-gray-900 mb-4">
              Snooze Incident
            </h3>
            <p className="text-sm text-gray-600 mb-4">
              Temporarily snooze this incident. It will return to active status after
              the selected time.
            </p>

            <div className="mb-6">
              <label className="block text-sm font-medium text-gray-700 mb-2">
                Snooze Duration
              </label>
              <div className="grid grid-cols-3 gap-2">
                {[15, 30, 60, 120, 240, 480].map((minutes) => (
                  <button
                    key={minutes}
                    onClick={() => setSnoozeMinutes(minutes)}
                    className={`px-4 py-2 rounded-md border ${
                      snoozeMinutes === minutes
                        ? 'bg-blue-600 text-white border-blue-600'
                        : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
                    }`}
                  >
                    {minutes < 60 ? `${minutes}m` : `${minutes / 60}h`}
                  </button>
                ))}
              </div>

              <div className="mt-4">
                <label className="block text-sm font-medium text-gray-700 mb-2">
                  Or enter custom minutes
                </label>
                <input
                  type="number"
                  min="1"
                  max="1440"
                  value={snoozeMinutes}
                  onChange={(e) => setSnoozeMinutes(parseInt(e.target.value) || 30)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            </div>

            <div className="flex gap-3">
              <button
                onClick={handleSnooze}
                className="flex-1 px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors"
              >
                Snooze for {snoozeMinutes < 60 ? `${snoozeMinutes}m` : `${snoozeMinutes / 60}h`}
              </button>
              <button
                onClick={() => setShowSnoozeDialog(false)}
                className="flex-1 px-4 py-2 bg-gray-200 text-gray-700 rounded-md hover:bg-gray-300 transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
