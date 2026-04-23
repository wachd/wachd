'use client';

import { useEffect, useState, useCallback, useMemo } from 'react';
import { api } from '@/lib/api';
import { useSession } from '@/lib/session-context';
import type { Schedule, OnCallUser, TeamMember, ScheduleOverride } from '@/lib/types';

// ── Types ─────────────────────────────────────────────────────────────────────

type TimelineEntry = {
  date: string;
  layers: Array<{ layer_name: string; user_id: string; user_name: string }>;
  override?: { id: string; user_id: string; user_name: string; reason?: string; start_at: string; end_at: string };
  final_user_id: string;
  final_user_name: string;
};

type TimelineData = {
  schedule_name: string;
  layer_names: string[];
  from: string;
  days: number;
  entries: TimelineEntry[];
};

type AdminTab = 'members' | 'schedule';
type ViewDays = 7 | 14;
type ScheduleMode = 'rotation' | 'manual';
type RotationInterval = 'daily' | 'weekly' | 'biweekly' | 'monthly';
type TimeRestriction = 'always' | 'weekdays' | 'weekends';

const INTERVAL_HOURS: Record<RotationInterval, number> = {
  daily: 24, weekly: 168, biweekly: 336, monthly: 720,
};
const INTERVAL_LABELS: Record<RotationInterval, string> = {
  daily: 'Daily', weekly: 'Weekly', biweekly: 'Bi-weekly', monthly: 'Monthly (~30 days)',
};

const DAYS = ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'] as const;
type Day = (typeof DAYS)[number];
const DAY_LABELS: Record<Day, string> = {
  monday: 'Monday', tuesday: 'Tuesday', wednesday: 'Wednesday', thursday: 'Thursday',
  friday: 'Friday', saturday: 'Saturday', sunday: 'Sunday',
};
const WEEKDAY_ABBR = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTH_ABBR = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const USER_COLORS: Array<{ bar: string; label: string }> = [
  { bar: 'bg-blue-300',   label: 'text-blue-900' },
  { bar: 'bg-green-300',  label: 'text-green-900' },
  { bar: 'bg-violet-300', label: 'text-violet-900' },
  { bar: 'bg-orange-300', label: 'text-orange-900' },
  { bar: 'bg-pink-300',   label: 'text-pink-900' },
  { bar: 'bg-teal-300',   label: 'text-teal-900' },
  { bar: 'bg-indigo-300', label: 'text-indigo-900' },
  { bar: 'bg-rose-300',   label: 'text-rose-900' },
];

// ── Helpers ───────────────────────────────────────────────────────────────────

function todayStr(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function shiftDate(dateStr: string, n: number): string {
  const [y, m, d] = dateStr.split('-').map(Number);
  const dt = new Date(y, m - 1, d + n);
  return `${dt.getFullYear()}-${String(dt.getMonth() + 1).padStart(2, '0')}-${String(dt.getDate()).padStart(2, '0')}`;
}

function parseDateParts(dateStr: string) {
  const [y, m, d] = dateStr.split('-').map(Number);
  const dt = new Date(y, m - 1, d);
  return { abbr: WEEKDAY_ABBR[dt.getDay()], day: d, month: MONTH_ABBR[m - 1] };
}

type Seg = { userId: string; userName: string; span: number };

function mergeToSegments(
  entries: TimelineEntry[],
  userId: (e: TimelineEntry) => string,
  userName: (e: TimelineEntry) => string,
): Seg[] {
  if (entries.length === 0) return [];
  const out: Seg[] = [];
  let cur: Seg = { userId: userId(entries[0]), userName: userName(entries[0]), span: 1 };
  for (let i = 1; i < entries.length; i++) {
    const uid = userId(entries[i]);
    if (uid === cur.userId) {
      cur.span++;
    } else {
      out.push(cur);
      cur = { userId: uid, userName: userName(entries[i]), span: 1 };
    }
  }
  out.push(cur);
  return out;
}

// ── Component ─────────────────────────────────────────────────────────────────

export default function OnCallPage() {
  const { session, primaryTeamId, loading: sessionLoading } = useSession();

  // Base data
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [currentOnCall, setCurrentOnCall] = useState<OnCallUser | null>(null);
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [overrides, setOverrides] = useState<ScheduleOverride[]>([]);
  const [baseLoading, setBaseLoading] = useState(true);
  const [baseError, setBaseError] = useState<string | null>(null);

  // Timeline
  const [timeline, setTimeline] = useState<TimelineData | null>(null);
  const [timelineLoading, setTimelineLoading] = useState(false);
  const [viewStart, setViewStart] = useState('');
  const [viewDays, setViewDays] = useState<ViewDays>(14);

  // Admin panel
  const [adminTab, setAdminTab] = useState<AdminTab>('members');

  // Override form
  const [showOverrideForm, setShowOverrideForm] = useState(false);
  const [overrideForm, setOverrideForm] = useState({ user_id: '', start_at: '', end_at: '', reason: '' });
  const [overrideLoading, setOverrideLoading] = useState(false);
  const [overrideError, setOverrideError] = useState<string | null>(null);

  // Phone editing
  const [editingPhone, setEditingPhone] = useState<string | null>(null);
  const [phoneValue, setPhoneValue] = useState('');
  const [phoneSaving, setPhoneSaving] = useState(false);
  const [phoneError, setPhoneError] = useState<string | null>(null);

  // Schedule edit form — common
  const [scheduleName, setScheduleName] = useState('Primary On-Call');
  const [scheduleMode, setScheduleMode] = useState<ScheduleMode>('rotation');
  const [scheduleLoading, setScheduleLoading] = useState(false);
  const [scheduleError, setScheduleError] = useState<string | null>(null);
  const [scheduleSaved, setScheduleSaved] = useState(false);

  // Schedule edit form — automatic rotation (LayeredRotation)
  const [rotMembers, setRotMembers] = useState<string[]>([]);
  const [rotInterval, setRotInterval] = useState<RotationInterval>('weekly');
  const [rotStart, setRotStart] = useState('');
  const [rotRestriction, setRotRestriction] = useState<TimeRestriction>('always');

  // Schedule edit form — manual day assignment (WeeklyRotation)
  const [rotation, setRotation] = useState<Record<Day, string>>({
    monday: '', tuesday: '', wednesday: '', thursday: '',
    friday: '', saturday: '', sunday: '',
  });

  const isAdmin =
    session?.isSuperAdmin ||
    (primaryTeamId != null && session?.roles[primaryTeamId] === 'admin');

  // Stable color assignment per member (by list position)
  const colorMap = useMemo<Record<string, { bar: string; label: string }>>(() => {
    const m: Record<string, { bar: string; label: string }> = {};
    members.forEach((mem, i) => { m[mem.id] = USER_COLORS[i % USER_COLORS.length]; });
    return m;
  }, [members]);

  function colorFor(userId: string) {
    return colorMap[userId] ?? { bar: 'bg-gray-200', label: 'text-gray-500' };
  }

  // ── Fetch ──────────────────────────────────────────────────────────────────

  const fetchBase = useCallback(async () => {
    if (!primaryTeamId) return;
    try {
      const [schedData, onCallData, membersData, overridesData] = await Promise.all([
        api.schedule.get(primaryTeamId),
        api.schedule.getCurrentOnCall(primaryTeamId),
        api.members.list(primaryTeamId),
        api.schedule.listOverrides(primaryTeamId),
      ]);
      setSchedule(schedData);
      setCurrentOnCall(onCallData);
      setMembers(membersData);
      setOverrides(overridesData);
      if (schedData) {
        setScheduleName(schedData.name || 'Primary On-Call');
        const cfg = schedData.rotation_config;
        if (cfg?.type === 'layered' && Array.isArray(cfg.layers) && cfg.layers.length > 0) {
          setScheduleMode('rotation');
          const layer = cfg.layers[0];
          setRotMembers(Array.isArray(layer.members) ? layer.members : []);
          const hours: number = cfg.rotation_interval_hours ?? 168;
          if (hours <= 24) setRotInterval('daily');
          else if (hours <= 168) setRotInterval('weekly');
          else if (hours <= 336) setRotInterval('biweekly');
          else setRotInterval('monthly');
          setRotStart(cfg.rotation_start ? cfg.rotation_start.slice(0, 16) : '');
          setRotRestriction(layer.time_restriction?.type === 'weekdays' ? 'weekdays' :
            layer.time_restriction?.type === 'weekends' ? 'weekends' : 'always');
        } else if (cfg?.type === 'weekly' && Array.isArray(cfg.rotation)) {
          setScheduleMode('manual');
          const r: Record<Day, string> = { monday: '', tuesday: '', wednesday: '', thursday: '', friday: '', saturday: '', sunday: '' };
          for (const e of cfg.rotation) {
            if (e.day && DAYS.includes(e.day as Day)) r[e.day as Day] = e.user_id || '';
          }
          setRotation(r);
        }
      }
    } catch (err) {
      setBaseError(err instanceof Error ? err.message : 'Failed to fetch data');
    } finally {
      setBaseLoading(false);
    }
  }, [primaryTeamId]);

  const fetchTimeline = useCallback(async (from: string, days: number) => {
    if (!primaryTeamId || !from) return;
    setTimelineLoading(true);
    try {
      const data = await api.schedule.getTimeline(primaryTeamId, from, days);
      setTimeline(data);
    } catch {
      setTimeline(null);
    } finally {
      setTimelineLoading(false);
    }
  }, [primaryTeamId]);

  useEffect(() => { setViewStart(todayStr()); }, []);

  useEffect(() => {
    if (sessionLoading) return;
    if (!primaryTeamId) {
      setBaseError('No team access — contact your administrator.');
      setBaseLoading(false);
      return;
    }
    fetchBase();
  }, [primaryTeamId, sessionLoading, fetchBase]);

  useEffect(() => {
    if (!viewStart) return;
    fetchTimeline(viewStart, viewDays);
  }, [viewStart, viewDays, fetchTimeline]);

  // ── Handlers ───────────────────────────────────────────────────────────────

  function startEditPhone(m: TeamMember) {
    setEditingPhone(m.id);
    setPhoneValue(m.phone || '');
    setPhoneError(null);
  }

  async function savePhone(m: TeamMember) {
    if (!primaryTeamId) return;
    setPhoneSaving(true);
    setPhoneError(null);
    try {
      await api.members.updatePhone(primaryTeamId, m.id, m.source, phoneValue || null);
      setEditingPhone(null);
      fetchBase();
    } catch (err) {
      setPhoneError(err instanceof Error ? err.message : 'Failed to update phone');
    } finally {
      setPhoneSaving(false);
    }
  }

  async function saveSchedule() {
    if (!primaryTeamId) return;
    setScheduleLoading(true);
    setScheduleError(null);
    setScheduleSaved(false);
    try {
      let rotation_config: Record<string, unknown>;
      if (scheduleMode === 'rotation') {
        if (rotMembers.length < 2) {
          setScheduleError('Add at least 2 members to the rotation.');
          setScheduleLoading(false);
          return;
        }
        const anchor = rotStart
          ? new Date(rotStart).toISOString()
          : new Date().toISOString();
        rotation_config = {
          type: 'layered',
          rotation_start: anchor,
          rotation_interval_hours: INTERVAL_HOURS[rotInterval],
          layers: [{
            id: 'layer-1',
            name: 'On-Call',
            layer_order: 1,
            time_restriction: { type: rotRestriction },
            members: rotMembers,
          }],
        };
      } else {
        const entries = DAYS.filter(d => rotation[d]).map(d => ({ day: d, user_id: rotation[d] }));
        rotation_config = { type: 'weekly', rotation: entries };
      }
      await api.schedule.upsert(primaryTeamId, {
        name: scheduleName || 'Primary On-Call',
        rotation_config,
        enabled: true,
      });
      setScheduleSaved(true);
      setTimeout(() => setScheduleSaved(false), 3000);
      await fetchBase();
      if (viewStart) fetchTimeline(viewStart, viewDays);
    } catch (err) {
      setScheduleError(err instanceof Error ? err.message : 'Failed to save schedule');
    } finally {
      setScheduleLoading(false);
    }
  }

  async function createOverride() {
    if (!primaryTeamId) return;
    setOverrideLoading(true);
    setOverrideError(null);
    try {
      await api.schedule.createOverride(primaryTeamId, {
        user_id: overrideForm.user_id,
        start_at: new Date(overrideForm.start_at).toISOString(),
        end_at: new Date(overrideForm.end_at).toISOString(),
        reason: overrideForm.reason || undefined,
      });
      setShowOverrideForm(false);
      setOverrideForm({ user_id: '', start_at: '', end_at: '', reason: '' });
      await fetchBase();
      if (viewStart) fetchTimeline(viewStart, viewDays);
    } catch (err) {
      setOverrideError(err instanceof Error ? err.message : 'Failed to create override');
    } finally {
      setOverrideLoading(false);
    }
  }

  async function deleteOverride(id: string) {
    if (!primaryTeamId || !confirm('Delete this override?')) return;
    try {
      await api.schedule.deleteOverride(primaryTeamId, id);
      await fetchBase();
      if (viewStart) fetchTimeline(viewStart, viewDays);
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to delete override');
    }
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  if (baseLoading) {
    return (
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="animate-pulse space-y-4">
          <div className="h-8 bg-gray-200 rounded w-1/4" />
          <div className="h-48 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  if (baseError) {
    return (
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="bg-red-50 border border-red-200 rounded-lg p-4">
          <h3 className="text-red-800 font-medium">Error</h3>
          <p className="text-red-600 text-sm mt-1">{baseError}</p>
        </div>
      </div>
    );
  }

  const today = todayStr();
  const entries = timeline?.entries ?? [];
  const layerNames = timeline?.layer_names ?? [];

  // Build segment data for the grid
  const layerSegments = layerNames.map((_, li) =>
    mergeToSegments(entries, e => e.layers[li]?.user_id ?? '', e => e.layers[li]?.user_name ?? '')
  );
  const finalSegments = mergeToSegments(entries, e => e.final_user_id, e => e.final_user_name);
  const hasOverrides = entries.some(e => e.override);

  return (
    <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-8 space-y-6">

      {/* ── Page header ──────────────────────────────────────────────────── */}
      <div className="flex items-start justify-between">
        <h1 className="text-2xl font-bold text-gray-900">On-Call Schedule</h1>
        {currentOnCall && (
          <div className="flex items-center gap-2 bg-green-50 border border-green-200 rounded-lg px-4 py-2">
            <span className="w-2 h-2 rounded-full bg-green-500 animate-pulse flex-shrink-0" />
            <div className="text-sm">
              <span className="text-gray-500">On-call now: </span>
              <span className="font-semibold text-gray-900">{currentOnCall.user.name}</span>
              {currentOnCall.user.email && (
                <span className="text-gray-500 ml-2">{currentOnCall.user.email}</span>
              )}
            </div>
          </div>
        )}
        {!currentOnCall && !schedule && (
          <div className="flex items-center gap-2 bg-yellow-50 border border-yellow-200 rounded-lg px-4 py-2">
            <span className="text-sm text-yellow-800">No schedule configured — scroll down to set one up</span>
          </div>
        )}
        {!currentOnCall && schedule && (
          <div className="flex items-center gap-2 bg-gray-50 border border-gray-200 rounded-lg px-4 py-2">
            <span className="text-sm text-gray-500">No coverage right now</span>
          </div>
        )}
      </div>

      {/* ── Timeline card ────────────────────────────────────────────────── */}
      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">

        {/* Card header */}
        <div className="px-5 py-3 border-b border-gray-200 flex items-center justify-between flex-wrap gap-3">
          <div className="flex items-center gap-3">
            <h2 className="text-base font-semibold text-gray-900">
              {timeline?.schedule_name || schedule?.name || 'Schedule Timeline'}
            </h2>
            {timelineLoading && (
              <span className="text-xs text-gray-400 animate-pulse">Loading…</span>
            )}
          </div>

          <div className="flex items-center gap-2">
            {/* View width toggle */}
            <div className="flex rounded border border-gray-300 overflow-hidden text-xs font-medium">
              {([7, 14] as ViewDays[]).map(d => (
                <button
                  key={d}
                  onClick={() => setViewDays(d)}
                  className={`px-2.5 py-1 transition-colors ${
                    viewDays === d ? 'bg-blue-600 text-white' : 'bg-white text-gray-600 hover:bg-gray-50'
                  }`}
                >
                  {d}d
                </button>
              ))}
            </div>

            {/* Navigation */}
            <button
              onClick={() => setViewStart(s => shiftDate(s, -viewDays))}
              className="p-1.5 rounded border border-gray-300 hover:bg-gray-50 text-gray-600 text-sm leading-none"
              title="Previous period"
            >
              ‹
            </button>
            <button
              onClick={() => setViewStart(todayStr())}
              className="px-2.5 py-1 rounded border border-gray-300 text-xs font-medium text-gray-600 hover:bg-gray-50"
            >
              Today
            </button>
            <button
              onClick={() => setViewStart(s => shiftDate(s, viewDays))}
              className="p-1.5 rounded border border-gray-300 hover:bg-gray-50 text-gray-600 text-sm leading-none"
              title="Next period"
            >
              ›
            </button>

            {/* Add Override */}
            {isAdmin && schedule && (
              <button
                onClick={() => { setShowOverrideForm(v => !v); setOverrideError(null); }}
                className="ml-1 px-3 py-1 rounded border border-dashed border-blue-400 text-xs font-medium text-blue-600 hover:bg-blue-50 transition-colors"
              >
                + Override
              </button>
            )}
          </div>
        </div>

        {/* Override form (inline) */}
        {showOverrideForm && isAdmin && (
          <div className="border-b border-gray-200 bg-blue-50 px-5 py-4">
            <h3 className="text-sm font-semibold text-gray-800 mb-3">Add Override</h3>
            {overrideError && <p className="text-red-600 text-xs mb-2">{overrideError}</p>}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <div className="col-span-2 sm:col-span-1">
                <label className="block text-xs font-medium text-gray-600 mb-1">Cover by *</label>
                <select
                  value={overrideForm.user_id}
                  onChange={e => setOverrideForm({ ...overrideForm, user_id: e.target.value })}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                >
                  <option value="">Select member…</option>
                  {members.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
                </select>
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Start *</label>
                <input type="datetime-local" value={overrideForm.start_at}
                  onChange={e => setOverrideForm({ ...overrideForm, start_at: e.target.value })}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500" />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">End *</label>
                <input type="datetime-local" value={overrideForm.end_at}
                  onChange={e => setOverrideForm({ ...overrideForm, end_at: e.target.value })}
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500" />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Reason</label>
                <input type="text" value={overrideForm.reason}
                  onChange={e => setOverrideForm({ ...overrideForm, reason: e.target.value })}
                  placeholder="Vacation, sick…"
                  className="w-full border border-gray-300 rounded px-2 py-1.5 text-xs focus:outline-none focus:ring-1 focus:ring-blue-500" />
              </div>
            </div>
            <div className="flex gap-2 mt-3">
              <button
                onClick={createOverride}
                disabled={overrideLoading || !overrideForm.user_id || !overrideForm.start_at || !overrideForm.end_at}
                className="px-3 py-1.5 text-xs font-medium bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
              >
                {overrideLoading ? 'Saving…' : 'Save Override'}
              </button>
              <button
                onClick={() => setShowOverrideForm(false)}
                className="px-3 py-1.5 text-xs font-medium text-gray-600 bg-white border border-gray-300 rounded hover:bg-gray-50"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        {/* Timeline grid */}
        {entries.length === 0 && !timelineLoading && (
          <div className="px-5 py-12 text-center">
            <p className="text-gray-400 text-sm">
              {schedule ? 'No data for this period.' : 'No schedule configured yet.'}
            </p>
            {isAdmin && !schedule && (
              <button
                onClick={() => setAdminTab('schedule')}
                className="mt-2 text-xs text-blue-600 hover:text-blue-700 font-medium"
              >
                Set up schedule below ↓
              </button>
            )}
          </div>
        )}

        {entries.length > 0 && (
          <div className="overflow-x-auto">
            <table className="border-separate border-spacing-0 w-max min-w-full">
              <colgroup>
                <col style={{ minWidth: 120, width: 120 }} />
                {entries.map(e => <col key={e.date} style={{ minWidth: 76, width: 76 }} />)}
              </colgroup>

              {/* Date header */}
              <thead>
                <tr>
                  <th className="sticky left-0 z-10 bg-white border-b border-gray-200" />
                  {entries.map(e => {
                    const { abbr, day, month } = parseDateParts(e.date);
                    const isToday = e.date === today;
                    return (
                      <th
                        key={e.date}
                        className={`px-1 py-2 text-center border-b border-gray-200 font-normal ${isToday ? 'bg-blue-50' : 'bg-white'}`}
                      >
                        <div className={`text-[10px] font-medium uppercase tracking-wide ${isToday ? 'text-blue-600' : 'text-gray-400'}`}>
                          {abbr}
                        </div>
                        <div className={`text-xs font-bold leading-tight ${isToday ? 'text-blue-700' : 'text-gray-700'}`}>
                          {day}
                        </div>
                        <div className={`text-[10px] ${isToday ? 'text-blue-500' : 'text-gray-400'}`}>
                          {month}
                        </div>
                        {isToday && (
                          <div className="mt-0.5 mx-auto w-1.5 h-1.5 rounded-full bg-blue-500" />
                        )}
                      </th>
                    );
                  })}
                </tr>
              </thead>

              <tbody>
                {/* ── Rotation layers ──────────────────────────────────── */}
                {layerNames.map((name, li) => (
                  <tr key={name}>
                    <td className="sticky left-0 z-10 bg-white px-3 py-1.5 border-b border-gray-100">
                      <span className="text-xs font-medium text-gray-500 truncate block max-w-[108px]">{name}</span>
                    </td>
                    {layerSegments[li].map((seg, si) => {
                      const c = seg.userId ? colorFor(seg.userId) : null;
                      return (
                        <td key={si} colSpan={seg.span} className="px-0.5 py-1.5 border-b border-gray-100 align-middle">
                          {seg.userId ? (
                            <div className={`rounded px-2 py-1 text-[11px] font-medium truncate ${c!.bar} ${c!.label}`}>
                              {seg.userName}
                            </div>
                          ) : (
                            <div className="rounded px-2 py-1 text-[11px] text-gray-300 bg-gray-50 text-center">—</div>
                          )}
                        </td>
                      );
                    })}
                  </tr>
                ))}

                {/* ── Overrides row ─────────────────────────────────────── */}
                {hasOverrides && (
                  <tr>
                    <td className="sticky left-0 z-10 bg-white px-3 py-1.5 border-b border-gray-100">
                      <span className="text-xs font-medium text-amber-600">Overrides</span>
                    </td>
                    {entries.map(e => {
                      const ov = e.override;
                      if (!ov) return (
                        <td key={e.date} className="px-0.5 py-1.5 border-b border-gray-100 align-middle">
                          <div className="rounded px-2 py-1 text-[11px] text-gray-200 bg-gray-50 text-center">—</div>
                        </td>
                      );
                      const dayStart = new Date(e.date + 'T00:00:00Z').getTime();
                      const dayEnd   = dayStart + 86_400_000;
                      const ovStart  = Math.max(new Date(ov.start_at).getTime(), dayStart);
                      const ovEnd    = Math.min(new Date(ov.end_at).getTime(),   dayEnd);
                      const leftPct  = ((ovStart - dayStart) / 86_400_000) * 100;
                      const widthPct = Math.max(((ovEnd - ovStart) / 86_400_000) * 100, 8);
                      const startFmt = new Date(ov.start_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
                      const endFmt   = new Date(ov.end_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
                      return (
                        <td key={e.date} className="px-0.5 py-1.5 border-b border-gray-100 align-middle">
                          <div className="relative h-6 overflow-hidden">
                            <div
                              className="absolute top-0 bottom-0 rounded px-1 flex items-center text-[11px] font-medium truncate bg-amber-200 text-amber-900"
                              style={{ left: `${leftPct}%`, width: `${widthPct}%` }}
                              title={`${ov.user_name}${ov.reason ? ` — ${ov.reason}` : ''} · ${startFmt}–${endFmt}`}
                            >
                              {ov.user_name}
                            </div>
                          </div>
                        </td>
                      );
                    })}
                  </tr>
                )}

                {/* ── Spacer ────────────────────────────────────────────── */}
                <tr>
                  <td colSpan={entries.length + 1} className="h-1 border-b border-gray-300 bg-gray-50" />
                </tr>

                {/* ── Final schedule ────────────────────────────────────── */}
                <tr className="bg-gray-50">
                  <td className="sticky left-0 z-10 bg-gray-50 px-3 py-2 border-b border-gray-200">
                    <span className="text-[10px] font-bold uppercase tracking-widest text-gray-500">Final</span>
                  </td>
                  {entries.map((e, ei) => {
                    const ov = e.override;
                    // Determine if the override covers only part of the day.
                    const isPartial = ov && (() => {
                      const dayStart = new Date(e.date + 'T00:00:00Z').getTime();
                      const dayEnd   = dayStart + 86_400_000;
                      return new Date(ov.start_at).getTime() > dayStart ||
                             new Date(ov.end_at).getTime()   < dayEnd;
                    })();

                    if (isPartial && ov) {
                      // Partial override — base user fills cell, override segment overlays it.
                      const dayStart  = new Date(e.date + 'T00:00:00Z').getTime();
                      const dayEnd    = dayStart + 86_400_000;
                      const ovStart   = Math.max(new Date(ov.start_at).getTime(), dayStart);
                      const ovEnd     = Math.min(new Date(ov.end_at).getTime(),   dayEnd);
                      const leftPct   = ((ovStart - dayStart) / 86_400_000) * 100;
                      const widthPct  = Math.max(((ovEnd - ovStart) / 86_400_000) * 100, 6);
                      const baseUid   = e.layers[0]?.user_id  ?? '';
                      const baseName  = e.layers[0]?.user_name ?? '';
                      const baseC     = baseUid ? colorFor(baseUid) : null;
                      const ovC       = colorFor(ov.user_id);
                      return (
                        <td key={ei} className="px-0.5 py-2 border-b border-gray-200 align-middle">
                          <div className="relative h-7 overflow-hidden">
                            {baseC && (
                              <div className={`absolute inset-0 rounded px-2 flex items-center text-[11px] font-bold truncate ${baseC.bar} ${baseC.label}`}>
                                {baseName}
                              </div>
                            )}
                            <div
                              className={`absolute top-0 bottom-0 rounded px-1 flex items-center text-[11px] font-bold truncate ${ovC.bar} ${ovC.label}`}
                              style={{ left: `${leftPct}%`, width: `${widthPct}%` }}
                              title={`Override: ${ov.user_name}${ov.reason ? ` — ${ov.reason}` : ''}`}
                            >
                              {ov.user_name}
                            </div>
                          </div>
                        </td>
                      );
                    }

                    // Full-day override or no override — single bar (preserve colSpan via segments).
                    const c = e.final_user_id ? colorFor(e.final_user_id) : null;
                    return (
                      <td key={ei} className="px-0.5 py-2 border-b border-gray-200 align-middle">
                        {e.final_user_id ? (
                          <div className={`rounded px-2 py-1.5 text-[11px] font-bold truncate ${c!.bar} ${c!.label}`}>
                            {e.final_user_name}
                          </div>
                        ) : (
                          <div className="rounded px-2 py-1.5 text-[11px] text-gray-300 bg-gray-100 text-center">—</div>
                        )}
                      </td>
                    );
                  })}
                </tr>
              </tbody>
            </table>
          </div>
        )}

        {/* Legend */}
        {members.length > 0 && entries.length > 0 && (
          <div className="px-5 py-3 border-t border-gray-100 flex flex-wrap gap-3">
            {members.map((m, i) => {
              const c = USER_COLORS[i % USER_COLORS.length];
              return (
                <div key={m.id} className="flex items-center gap-1.5">
                  <div className={`w-2.5 h-2.5 rounded-sm ${c.bar}`} />
                  <span className="text-xs text-gray-600">{m.name}</span>
                </div>
              );
            })}
            {hasOverrides && (
              <div className="flex items-center gap-1.5">
                <div className="w-2.5 h-2.5 rounded-sm bg-amber-200" />
                <span className="text-xs text-gray-500">Override</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* ── Upcoming overrides list ───────────────────────────────────────── */}
      {isAdmin && (
        <div className="bg-white rounded-lg border border-gray-200">
          <div className="px-5 py-3 border-b border-gray-200 flex items-center justify-between">
            <h2 className="text-sm font-semibold text-gray-900">
              Upcoming Overrides {overrides.length > 0 && <span className="text-gray-400 font-normal">({overrides.length})</span>}
            </h2>
          </div>
          {overrides.length === 0 ? (
            <div className="px-5 py-6 text-center text-sm text-gray-400">No upcoming overrides.</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-100">
                  <tr>
                    <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Person</th>
                    <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Starts</th>
                    <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Ends</th>
                    <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Reason</th>
                    <th className="px-4 py-2.5" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {overrides.map(o => (
                    <tr key={o.id} className="hover:bg-gray-50">
                      <td className="px-4 py-2.5 font-medium text-gray-900">
                        {members.find(m => m.id === o.user_id)?.name || o.user_id}
                      </td>
                      <td className="px-4 py-2.5 text-gray-500">{new Date(o.start_at).toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-gray-500">{new Date(o.end_at).toLocaleString()}</td>
                      <td className="px-4 py-2.5 text-gray-500">{o.reason || '—'}</td>
                      <td className="px-4 py-2.5 text-right">
                        <button
                          onClick={() => deleteOverride(o.id)}
                          className="text-xs text-red-500 hover:text-red-700 font-medium"
                        >
                          Delete
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* ── Admin panel: Members + Edit Schedule ─────────────────────────── */}
      {isAdmin && (
        <div className="bg-white rounded-lg border border-gray-200">
          {/* Tabs */}
          <div className="border-b border-gray-200 px-5">
            <nav className="-mb-px flex gap-6">
              {(['members', 'schedule'] as AdminTab[]).map(tab => (
                <button
                  key={tab}
                  onClick={() => setAdminTab(tab)}
                  className={`py-3 px-1 text-sm font-medium border-b-2 capitalize transition-colors ${
                    adminTab === tab
                      ? 'border-blue-600 text-blue-600'
                      : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
                  }`}
                >
                  {tab === 'members' ? `Members (${members.length})` : 'Edit Schedule'}
                </button>
              ))}
            </nav>
          </div>

          {/* Members tab */}
          {adminTab === 'members' && (
            <div className="p-5">
              <div className="bg-blue-50 border border-blue-100 rounded px-4 py-2.5 text-xs text-blue-700 mb-4">
                Members come from group access. To add or remove members, go to{' '}
                <strong>Admin → Groups</strong>. Here you can set phone numbers for SMS/voice alerts.
              </div>
              {members.length === 0 ? (
                <p className="text-gray-400 text-sm text-center py-6">No team members yet.</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead className="bg-gray-50 border-b border-gray-200">
                      <tr>
                        <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Name</th>
                        <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Email</th>
                        <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Phone</th>
                        <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Role</th>
                        <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Auth</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                      {members.map(m => (
                        <tr key={m.id} className="group hover:bg-gray-50">
                          <td className="px-4 py-2.5 font-medium text-gray-900">{m.name}</td>
                          <td className="px-4 py-2.5 text-gray-500">{m.email}</td>
                          <td className="px-4 py-2.5">
                            {editingPhone === m.id ? (
                              <div className="flex items-center gap-2">
                                <input
                                  type="tel"
                                  value={phoneValue}
                                  onChange={e => setPhoneValue(e.target.value)}
                                  className="border border-gray-300 rounded px-2 py-1 text-xs w-36 focus:outline-none focus:ring-1 focus:ring-blue-500"
                                  placeholder="+1 555 000 0000"
                                  autoFocus
                                />
                                <button onClick={() => savePhone(m)} disabled={phoneSaving} className="text-xs text-blue-600 font-medium">
                                  {phoneSaving ? '…' : 'Save'}
                                </button>
                                <button onClick={() => setEditingPhone(null)} className="text-xs text-gray-400">Cancel</button>
                                {phoneError && <span className="text-red-500 text-xs">{phoneError}</span>}
                              </div>
                            ) : (
                              <div className="flex items-center gap-2">
                                <span className="text-gray-500">{m.phone || '—'}</span>
                                <button
                                  onClick={() => startEditPhone(m)}
                                  className="text-xs text-blue-500 opacity-0 group-hover:opacity-100"
                                >
                                  Edit
                                </button>
                              </div>
                            )}
                          </td>
                          <td className="px-4 py-2.5">
                            <span className={`inline-flex px-2 py-0.5 rounded text-xs font-medium ${
                              m.role === 'admin' ? 'bg-purple-100 text-purple-800' :
                              m.role === 'responder' ? 'bg-green-100 text-green-800' :
                              'bg-gray-100 text-gray-600'
                            }`}>
                              {m.role}
                            </span>
                          </td>
                          <td className="px-4 py-2.5">
                            <span className={`inline-flex px-2 py-0.5 rounded text-xs font-medium ${
                              m.source === 'sso' ? 'bg-blue-100 text-blue-700' : 'bg-gray-100 text-gray-600'
                            }`}>
                              {m.source}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          )}

          {/* Edit Schedule tab */}
          {adminTab === 'schedule' && (
            <div className="p-5 space-y-5">
              {members.length === 0 && (
                <div className="bg-yellow-50 border border-yellow-200 rounded p-3 text-xs text-yellow-800">
                  Add team members first via Admin → Groups.
                </div>
              )}

              {/* Schedule name */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Schedule name</label>
                <input
                  type="text"
                  value={scheduleName}
                  onChange={e => setScheduleName(e.target.value)}
                  className="border border-gray-300 rounded px-3 py-2 text-sm w-full max-w-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>

              {/* Mode selector */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-2">Rotation type</label>
                <div className="flex gap-3">
                  {(['rotation', 'manual'] as ScheduleMode[]).map(mode => (
                    <button
                      key={mode}
                      onClick={() => setScheduleMode(mode)}
                      className={`px-4 py-2 rounded-lg border text-sm font-medium transition-colors ${
                        scheduleMode === mode
                          ? 'bg-blue-600 text-white border-blue-600'
                          : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
                      }`}
                    >
                      {mode === 'rotation' ? 'Automatic rotation' : 'Manual (day-by-day)'}
                    </button>
                  ))}
                </div>
                <p className="text-xs text-gray-400 mt-2">
                  {scheduleMode === 'rotation'
                    ? 'Members cycle in order — daily, weekly, or monthly. Best for evenly distributed on-call.'
                    : 'Assign a specific person to each day of the week. Best for teams with uneven availability.'}
                </p>
              </div>

              {/* ── Automatic rotation ─────────────────────────────────── */}
              {scheduleMode === 'rotation' && (
                <div className="space-y-4">
                  {/* Rotation interval */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-2">Rotate every</label>
                    <div className="flex flex-wrap gap-2">
                      {(Object.keys(INTERVAL_LABELS) as RotationInterval[]).map(iv => (
                        <button
                          key={iv}
                          onClick={() => setRotInterval(iv)}
                          className={`px-3 py-1.5 rounded border text-sm transition-colors ${
                            rotInterval === iv
                              ? 'bg-blue-600 text-white border-blue-600'
                              : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
                          }`}
                        >
                          {INTERVAL_LABELS[iv]}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Active hours restriction */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-2">Active hours</label>
                    <div className="flex flex-wrap gap-2">
                      {([
                        ['always', 'Always (24/7)'],
                        ['weekdays', 'Weekdays only (Mon–Fri)'],
                        ['weekends', 'Weekends only (Sat–Sun)'],
                      ] as [TimeRestriction, string][]).map(([val, label]) => (
                        <button
                          key={val}
                          onClick={() => setRotRestriction(val)}
                          className={`px-3 py-1.5 rounded border text-sm transition-colors ${
                            rotRestriction === val
                              ? 'bg-blue-600 text-white border-blue-600'
                              : 'bg-white text-gray-700 border-gray-300 hover:bg-gray-50'
                          }`}
                        >
                          {label}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Rotation start anchor */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-1">
                      Rotation start{' '}
                      <span className="font-normal text-gray-400">(when does person #1 go first?)</span>
                    </label>
                    <input
                      type="datetime-local"
                      value={rotStart}
                      onChange={e => setRotStart(e.target.value)}
                      className="border border-gray-300 rounded px-3 py-2 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                    />
                    <p className="text-xs text-gray-400 mt-1">Leave blank to start now.</p>
                  </div>

                  {/* Member order */}
                  <div>
                    <label className="block text-sm font-medium text-gray-700 mb-2">
                      Rotation order{' '}
                      <span className="font-normal text-gray-400">(top = first on-call)</span>
                    </label>
                    {rotMembers.length === 0 && (
                      <p className="text-xs text-gray-400 mb-2">Add members below to build the rotation.</p>
                    )}
                    <div className="space-y-1 mb-2">
                      {rotMembers.map((uid, idx) => {
                        const m = members.find(x => x.id === uid);
                        return (
                          <div key={uid} className="flex items-center gap-2 bg-gray-50 rounded px-3 py-2">
                            <span className="text-xs text-gray-400 w-5 text-center">{idx + 1}</span>
                            <span className="text-sm text-gray-900 flex-1">{m?.name ?? uid}</span>
                            <button
                              onClick={() => {
                                if (idx === 0) return;
                                const next = [...rotMembers];
                                [next[idx - 1], next[idx]] = [next[idx], next[idx - 1]];
                                setRotMembers(next);
                              }}
                              disabled={idx === 0}
                              className="text-gray-400 hover:text-gray-700 disabled:opacity-20 text-xs px-1"
                              title="Move up"
                            >
                              ↑
                            </button>
                            <button
                              onClick={() => {
                                if (idx === rotMembers.length - 1) return;
                                const next = [...rotMembers];
                                [next[idx], next[idx + 1]] = [next[idx + 1], next[idx]];
                                setRotMembers(next);
                              }}
                              disabled={idx === rotMembers.length - 1}
                              className="text-gray-400 hover:text-gray-700 disabled:opacity-20 text-xs px-1"
                              title="Move down"
                            >
                              ↓
                            </button>
                            <button
                              onClick={() => setRotMembers(rotMembers.filter((_, i) => i !== idx))}
                              className="text-red-400 hover:text-red-600 text-xs px-1 ml-1"
                              title="Remove"
                            >
                              ×
                            </button>
                          </div>
                        );
                      })}
                    </div>
                    {/* Add member */}
                    <select
                      defaultValue=""
                      onChange={e => {
                        const uid = e.target.value;
                        if (uid && !rotMembers.includes(uid)) {
                          setRotMembers([...rotMembers, uid]);
                        }
                        e.target.value = '';
                      }}
                      className="border border-dashed border-gray-300 rounded px-3 py-1.5 text-sm text-gray-600 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    >
                      <option value="" disabled>+ Add member to rotation…</option>
                      {members.filter(m => !rotMembers.includes(m.id)).map(m => (
                        <option key={m.id} value={m.id}>{m.name}</option>
                      ))}
                    </select>
                  </div>
                </div>
              )}

              {/* ── Manual day-by-day ─────────────────────────────────── */}
              {scheduleMode === 'manual' && (
                <div className="space-y-2">
                  <p className="text-sm text-gray-600 mb-3">Assign one person per day of the week:</p>
                  {DAYS.map(day => (
                    <div key={day} className="flex items-center gap-4">
                      <span className="w-24 text-sm text-gray-600">{DAY_LABELS[day]}</span>
                      <select
                        value={rotation[day]}
                        onChange={e => setRotation({ ...rotation, [day]: e.target.value })}
                        className="border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                      >
                        <option value="">— Not assigned —</option>
                        {members.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
                      </select>
                    </div>
                  ))}
                </div>
              )}

              {scheduleError && <p className="text-red-600 text-sm">{scheduleError}</p>}
              {scheduleSaved && <p className="text-green-600 text-sm font-medium">Schedule saved.</p>}
              <button
                onClick={saveSchedule}
                disabled={scheduleLoading}
                className="px-4 py-2 text-sm font-medium bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
              >
                {scheduleLoading ? 'Saving…' : schedule ? 'Update Schedule' : 'Create Schedule'}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
