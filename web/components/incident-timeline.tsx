'use client';

import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import type { TimelineEvent } from '@/lib/types';

interface Props {
  teamId: string;
  incidentId: string;
}

const KIND_CONFIG: Record<
  TimelineEvent['kind'],
  { label: string; color: string; dot: string }
> = {
  alert_fired:        { label: 'Alert fired',       color: 'text-red-600',    dot: 'bg-red-500' },
  commit:             { label: 'Commit',             color: 'text-gray-700',   dot: 'bg-blue-400' },
  log_spike:          { label: 'Error logs',         color: 'text-orange-600', dot: 'bg-orange-400' },
  analysis_complete:  { label: 'AI analysis',        color: 'text-purple-600', dot: 'bg-purple-500' },
  notification_sent:  { label: 'Notified',           color: 'text-blue-600',   dot: 'bg-blue-500' },
  acknowledged:       { label: 'Acknowledged',       color: 'text-green-600',  dot: 'bg-green-400' },
  resolved:           { label: 'Resolved',           color: 'text-green-700',  dot: 'bg-green-600' },
};

function formatTime(iso: string) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatDate(iso: string) {
  return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' });
}

export default function IncidentTimeline({ teamId, incidentId }: Props) {
  const [events, setEvents] = useState<TimelineEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.incidents.timeline(teamId, incidentId)
      .then(setEvents)
      .catch(() => setError('Failed to load timeline'))
      .finally(() => setLoading(false));
  }, [teamId, incidentId]);

  if (loading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h2 className="text-lg font-semibold text-gray-900 mb-4">Timeline</h2>
        <div className="animate-pulse space-y-4">
          {[1, 2, 3].map(i => (
            <div key={i} className="flex gap-3">
              <div className="w-3 h-3 rounded-full bg-gray-200 mt-1 flex-shrink-0" />
              <div className="flex-1 space-y-1">
                <div className="h-3 bg-gray-200 rounded w-1/3" />
                <div className="h-3 bg-gray-200 rounded w-2/3" />
              </div>
            </div>
          ))}
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h2 className="text-lg font-semibold text-gray-900 mb-2">Timeline</h2>
        <p className="text-sm text-red-600">{error}</p>
      </div>
    );
  }

  if (events.length === 0) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h2 className="text-lg font-semibold text-gray-900 mb-2">Timeline</h2>
        <p className="text-sm text-gray-500">No events recorded yet.</p>
      </div>
    );
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h2 className="text-lg font-semibold text-gray-900 mb-6">Timeline</h2>
      <ol className="relative">
        {events.map((event, idx) => {
          const cfg = KIND_CONFIG[event.kind] ?? {
            label: event.kind,
            color: 'text-gray-600',
            dot: 'bg-gray-400',
          };
          const isLast = idx === events.length - 1;
          return (
            <li key={idx} className="flex gap-4">
              {/* Dot + vertical line */}
              <div className="flex flex-col items-center">
                <span className={`w-3 h-3 rounded-full flex-shrink-0 mt-0.5 ${cfg.dot}`} />
                {!isLast && <span className="w-px flex-1 bg-gray-200 my-1" />}
              </div>

              {/* Content */}
              <div className={`pb-6 ${isLast ? '' : ''}`}>
                <p className={`text-sm font-medium ${cfg.color}`}>{event.title}</p>
                {event.detail && (
                  <p className="text-sm text-gray-600 mt-0.5 leading-snug">{event.detail}</p>
                )}
                {event.meta && Object.keys(event.meta).length > 0 && (
                  <div className="flex flex-wrap gap-2 mt-1">
                    {Object.entries(event.meta).map(([k, v]) => (
                      <span key={k} className="inline-flex items-center text-xs bg-gray-100 text-gray-500 rounded px-1.5 py-0.5">
                        {k}: {v}
                      </span>
                    ))}
                  </div>
                )}
                <p className="text-xs text-gray-400 mt-1">
                  {formatDate(event.time)} · {formatTime(event.time)}
                </p>
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
