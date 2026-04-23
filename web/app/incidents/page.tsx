'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { api } from '@/lib/api';
import { useSession } from '@/lib/session-context';
import type { Incident } from '@/lib/types';

function getSeverityColor(severity: string) {
  switch (severity) {
    case 'critical':
      return 'bg-red-100 text-red-800 border-red-200';
    case 'high':
      return 'bg-orange-100 text-orange-800 border-orange-200';
    case 'medium':
      return 'bg-yellow-100 text-yellow-800 border-yellow-200';
    case 'low':
      return 'bg-blue-100 text-blue-800 border-blue-200';
    default:
      return 'bg-gray-100 text-gray-800 border-gray-200';
  }
}

function getStatusColor(status: string) {
  switch (status) {
    case 'open':
      return 'bg-yellow-100 text-yellow-800';
    case 'acknowledged':
      return 'bg-blue-100 text-blue-800';
    case 'resolved':
      return 'bg-green-100 text-green-800';
    case 'snoozed':
      return 'bg-purple-100 text-purple-800';
    default:
      return 'bg-gray-100 text-gray-800';
  }
}

function formatDate(dateString: string) {
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMs / 3600000);
  const diffDays = Math.floor(diffMs / 86400000);

  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 7) return `${diffDays}d ago`;

  return date.toLocaleDateString();
}

export default function IncidentsPage() {
  const { primaryTeamId, loading: sessionLoading } = useSession();
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (sessionLoading) return;
    if (!primaryTeamId) {
      setError('No team access — contact your administrator to configure group mappings.');
      setLoading(false);
      return;
    }
    async function fetchIncidents() {
      try {
        const data = await api.incidents.list(primaryTeamId!);
        setIncidents(data);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to fetch incidents');
      } finally {
        setLoading(false);
      }
    }
    fetchIncidents();
  }, [primaryTeamId, sessionLoading]);

  if (loading) {
    return (
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="animate-pulse">
          <div className="h-8 bg-gray-200 rounded w-1/4 mb-6"></div>
          <div className="space-y-4">
            {[...Array(5)].map((_, i) => (
              <div key={i} className="h-24 bg-gray-200 rounded"></div>
            ))}
          </div>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4">
          <h3 className="text-red-800 font-medium">Error loading incidents</h3>
          <p className="text-red-600 text-sm mt-1">{error}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-gray-900">Incidents</h1>
        <p className="text-gray-600 mt-1">
          {incidents.length} incident{incidents.length !== 1 ? 's' : ''} total
        </p>
      </div>

      {incidents.length === 0 ? (
        <div className="space-y-4">
          {/* First-run onboarding card */}
          <div className="bg-blue-50 border border-blue-200 rounded-lg p-6">
            <h2 className="text-base font-semibold text-blue-900 mb-1">No incidents yet — here&apos;s how to get your first alert</h2>
            <p className="text-sm text-blue-700 mb-4">Complete these steps to receive your first AI-analyzed alert.</p>
            <ol className="space-y-3 text-sm text-blue-800">
              <li className="flex items-start gap-2">
                <span className="font-bold mt-0.5">1.</span>
                <span>
                  <strong>Configure notifications</strong> — go to{' '}
                  <Link href="/settings" className="underline hover:text-blue-600">Settings</Link> and add your Slack webhook or SMTP email so alerts reach you.
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="font-bold mt-0.5">2.</span>
                <span>
                  <strong>Set up your on-call schedule</strong> — go to{' '}
                  <Link href="/oncall" className="underline hover:text-blue-600">On-Call</Link> so Wachd knows who to page.
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="font-bold mt-0.5">3.</span>
                <span>
                  <strong>Point your monitoring tool at the webhook</strong> — find your webhook URL in{' '}
                  <Link href="/settings" className="underline hover:text-blue-600">Settings → Integrations</Link> and add it to Grafana, Datadog, or Prometheus.
                </span>
              </li>
              <li className="flex items-start gap-2">
                <span className="font-bold mt-0.5">4.</span>
                <span>
                  <strong>Set your personal notification rules</strong> — go to{' '}
                  <Link href="/profile" className="underline hover:text-blue-600">your profile</Link> to choose how you want to be reached (email now, voice after 10 min, etc).
                </span>
              </li>
            </ol>
          </div>
          <div className="bg-white rounded-lg border border-gray-200 p-8 text-center text-gray-400 text-sm">
            Incidents will appear here once alerts start firing.
          </div>
        </div>
      ) : (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
          <div className="divide-y divide-gray-200">
            {incidents.map((incident) => (
              <Link
                key={incident.id}
                href={`/incidents/${incident.id}`}
                className="block hover:bg-gray-50 transition-colors"
              >
                <div className="p-6">
                  <div className="flex items-start justify-between">
                    <div className="flex-1">
                      <div className="flex items-center gap-3 mb-2">
                        <span
                          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${getSeverityColor(
                            incident.severity
                          )}`}
                        >
                          {incident.severity}
                        </span>
                        <span
                          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${getStatusColor(
                            incident.status
                          )}`}
                        >
                          {incident.status}
                        </span>
                        <span className="text-sm text-gray-500">
                          {formatDate(incident.fired_at)}
                        </span>
                      </div>
                      <h3 className="text-lg font-medium text-gray-900 mb-1">
                        {incident.title}
                      </h3>
                      {incident.message && (
                        <p className="text-sm text-gray-600 line-clamp-2">
                          {incident.message}
                        </p>
                      )}
                      {incident.analysis?.root_cause && (
                        <div className="mt-3 bg-blue-50 border border-blue-200 rounded-md p-3">
                          <p className="text-sm text-blue-900">
                            <span className="font-medium">AI Analysis: </span>
                            {incident.analysis.root_cause}
                          </p>
                        </div>
                      )}
                    </div>
                    <div className="ml-4 flex-shrink-0">
                      <svg
                        className="h-5 w-5 text-gray-400"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth={2}
                          d="M9 5l7 7-7 7"
                        />
                      </svg>
                    </div>
                  </div>
                </div>
              </Link>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
