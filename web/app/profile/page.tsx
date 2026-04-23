'use client';

import { useEffect, useState } from 'react';
import { useSession } from '@/lib/session-context';
import { api, type UserNotificationRule, type CreateNotificationRuleInput } from '@/lib/api';

type EventType = 'new_alert' | 'ack' | 'resolve';
type Channel = 'email' | 'sms' | 'voice' | 'slack';

const EVENT_LABELS: Record<EventType, string> = {
  new_alert: 'New Alert',
  ack: 'Acknowledged Alert',
  resolve: 'Closed Alert',
};

const CHANNEL_ICONS: Record<Channel, string> = {
  email: '✉',
  sms: '💬',
  voice: '☎',
  slack: '⬡',
};

const CHANNEL_LABELS: Record<Channel, string> = {
  email: 'email',
  sms: 'sms',
  voice: 'voice',
  slack: 'slack',
};

function delayLabel(minutes: number): string {
  if (minutes === 0) return 'immediately';
  return `after ${minutes} minute${minutes === 1 ? '' : 's'}`;
}

export default function ProfilePage() {
  const { session, loading: sessionLoading } = useSession();

  const [rules, setRules] = useState<UserNotificationRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [expandedEvents, setExpandedEvents] = useState<Set<EventType>>(new Set(['new_alert']));

  // Add rule modal state
  const [showModal, setShowModal] = useState(false);
  const [modalEventType, setModalEventType] = useState<EventType>('new_alert');
  const [modalChannel, setModalChannel] = useState<Channel>('email');
  const [modalDelay, setModalDelay] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (sessionLoading) return;
    api.notificationRules
      .list()
      .then((res) => setRules(res.data ?? []))
      .catch((e) => setError(e.message ?? 'Failed to load notification rules'))
      .finally(() => setLoading(false));
  }, [sessionLoading]);

  function toggleEvent(e: EventType) {
    setExpandedEvents((prev) => {
      const next = new Set(prev);
      if (next.has(e)) next.delete(e);
      else next.add(e);
      return next;
    });
  }

  async function handleToggleEnabled(rule: UserNotificationRule) {
    const updated = await api.notificationRules.update(rule.id, { enabled: !rule.enabled });
    setRules((prev) => prev.map((r) => (r.id === updated.data.id ? updated.data : r)));
  }

  async function handleDelete(id: string) {
    await api.notificationRules.delete(id);
    setRules((prev) => prev.filter((r) => r.id !== id));
  }

  async function handleAddRule() {
    setSaving(true);
    setSaveError(null);
    const input: CreateNotificationRuleInput = {
      event_type: modalEventType,
      channel: modalChannel,
      delay_minutes: modalDelay,
      enabled: true,
    };
    try {
      const res = await api.notificationRules.create(input);
      setRules((prev) => [...prev, res.data]);
      setShowModal(false);
      setModalDelay(0);
    } catch (e: unknown) {
      setSaveError(e instanceof Error ? e.message : 'Failed to save rule');
    } finally {
      setSaving(false);
    }
  }

  if (sessionLoading || loading) {
    return (
      <div className="max-w-3xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="text-gray-500 text-sm">Loading…</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="max-w-3xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4 text-sm text-red-700">{error}</div>
      </div>
    );
  }

  const eventTypes: EventType[] = ['new_alert', 'ack', 'resolve'];

  return (
    <div className="max-w-3xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Notification Rules</h1>
          <p className="mt-1 text-sm text-gray-500">
            Configure how you want to be notified. Rules apply to you personally across all teams.
          </p>
        </div>
        <button
          onClick={() => { setShowModal(true); setSaveError(null); }}
          className="px-4 py-2 bg-blue-600 text-white text-sm font-medium rounded-md hover:bg-blue-700 transition-colors"
        >
          Add rule
        </button>
      </div>

      <div className="space-y-3">
        {eventTypes.map((eventType) => {
          const eventRules = rules.filter((r) => r.event_type === eventType);
          const expanded = expandedEvents.has(eventType);
          return (
            <div key={eventType} className="bg-white rounded-lg border border-gray-200">
              <button
                onClick={() => toggleEvent(eventType)}
                className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50 transition-colors rounded-lg"
              >
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold text-gray-900">{EVENT_LABELS[eventType]}</span>
                  <span className="text-xs bg-gray-100 text-gray-600 rounded-full px-2 py-0.5">
                    {eventRules.length}
                  </span>
                </div>
                <span className="text-gray-400 text-xs">{expanded ? '▲' : '▼'}</span>
              </button>

              {expanded && (
                <div className="border-t border-gray-100">
                  {eventRules.length === 0 ? (
                    <div className="px-4 py-4 text-sm text-gray-400 italic">
                      No rules — using default (all channels immediately).
                    </div>
                  ) : (
                    <ul className="divide-y divide-gray-100">
                      {eventRules.map((rule) => (
                        <li key={rule.id} className="flex items-center justify-between px-4 py-3">
                          <div className="flex items-center gap-3">
                            <span className="text-lg w-6 text-center text-gray-500">
                              {CHANNEL_ICONS[rule.channel as Channel]}
                            </span>
                            <span className={`text-sm ${rule.enabled ? 'text-gray-800' : 'text-gray-400 line-through'}`}>
                              Notify me via{' '}
                              <strong>{CHANNEL_LABELS[rule.channel as Channel]}</strong>{' '}
                              {session?.email ? (
                                <>at <strong>{session.email}</strong>{' '}</>
                              ) : null}
                              <span className="text-blue-600 font-medium">{delayLabel(rule.delay_minutes)}</span>
                            </span>
                          </div>
                          <div className="flex items-center gap-2">
                            <button
                              onClick={() => handleToggleEnabled(rule)}
                              title={rule.enabled ? 'Pause' : 'Resume'}
                              className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
                            >
                              {rule.enabled ? '⏸' : '▶'}
                            </button>
                            <button
                              onClick={() => handleDelete(rule.id)}
                              title="Delete"
                              className="p-1 text-gray-400 hover:text-red-600 transition-colors"
                            >
                              ✕
                            </button>
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Add rule modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50">
          <div className="bg-white rounded-xl shadow-xl w-full max-w-sm mx-4 p-6">
            <h2 className="text-lg font-semibold text-gray-900 mb-4">Add notification rule</h2>

            <div className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Event type</label>
                <select
                  value={modalEventType}
                  onChange={(e) => setModalEventType(e.target.value as EventType)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  {eventTypes.map((et) => (
                    <option key={et} value={et}>{EVENT_LABELS[et]}</option>
                  ))}
                </select>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Channel</label>
                <select
                  value={modalChannel}
                  onChange={(e) => setModalChannel(e.target.value as Channel)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  {(['email', 'sms', 'voice', 'slack'] as Channel[]).map((c) => (
                    <option key={c} value={c}>
                      {CHANNEL_ICONS[c]} {CHANNEL_LABELS[c]}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Delay (minutes) — 0 = immediately
                </label>
                <input
                  type="number"
                  min={0}
                  max={1440}
                  value={modalDelay}
                  onChange={(e) => setModalDelay(Math.max(0, Math.min(1440, parseInt(e.target.value) || 0)))}
                  className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
                <p className="mt-1 text-xs text-gray-400">
                  {modalDelay === 0
                    ? 'Will fire immediately when the event occurs.'
                    : `Will fire ${modalDelay} min after the event if the incident is still open.`}
                </p>
              </div>

              {saveError && (
                <div className="text-sm text-red-600 bg-red-50 border border-red-200 rounded p-2">{saveError}</div>
              )}
            </div>

            <div className="flex justify-end gap-3 mt-6">
              <button
                onClick={() => setShowModal(false)}
                className="px-4 py-2 text-sm text-gray-600 hover:text-gray-800 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleAddRule}
                disabled={saving}
                className="px-4 py-2 bg-blue-600 text-white text-sm font-medium rounded-md hover:bg-blue-700 disabled:opacity-50 transition-colors"
              >
                {saving ? 'Saving…' : 'Add rule'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
