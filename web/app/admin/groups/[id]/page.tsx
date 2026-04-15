'use client';

import { useEffect, useState, useCallback } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { api } from '@/lib/api';

interface LocalGroup {
  id: string;
  name: string;
  description?: string;
}

interface Member {
  id: string;
  username: string;
  name: string;
  email: string;
}

interface TeamAccess {
  team_id: string;
  team_name: string;
  role: string;
}

interface LocalUser {
  id: string;
  username: string;
  name: string;
  email: string;
}

interface Team {
  id: string;
  name: string;
}

type Tab = 'members' | 'access';

export default function GroupDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [tab, setTab] = useState<Tab>('members');
  const [group, setGroup] = useState<LocalGroup | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [access, setAccess] = useState<TeamAccess[]>([]);
  const [allUsers, setAllUsers] = useState<LocalUser[]>([]);
  const [allTeams, setAllTeams] = useState<Team[]>([]);
  const [loading, setLoading] = useState(true);

  // Add member form
  const [addingMember, setAddingMember] = useState(false);
  const [selectedUser, setSelectedUser] = useState('');
  const [memberError, setMemberError] = useState('');

  // Add access form
  const [addingAccess, setAddingAccess] = useState(false);
  const [accessForm, setAccessForm] = useState({ team_id: '', role: 'viewer' });
  const [accessError, setAccessError] = useState('');

  const load = useCallback(async () => {
    const [groupsData, membersData, accessData, usersData, teamsData] = await Promise.all([
      api.admin.groups.list() as unknown as { groups: LocalGroup[] },
      api.admin.groups.listMembers(id) as unknown as { members: Member[] },
      api.admin.groups.listAccess(id) as unknown as { access: TeamAccess[] },
      api.admin.users.list() as unknown as { users: LocalUser[] },
      api.admin.teams.list() as unknown as { teams: Team[] },
    ]);
    setGroup((groupsData.groups ?? []).find(g => g.id === id) ?? null);
    setMembers(membersData.members ?? []);
    setAccess(accessData.access ?? []);
    setAllUsers(usersData.users ?? []);
    setAllTeams(teamsData.teams ?? []);
    setLoading(false);
  }, [id]);

  useEffect(() => { load(); }, [load]);

  async function handleAddMember(e: React.FormEvent) {
    e.preventDefault();
    setMemberError('');
    try {
      await api.admin.groups.addMember(id, selectedUser);
      setSelectedUser('');
      setAddingMember(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setMemberError(JSON.parse(msg).error ?? msg); } catch { setMemberError(msg); }
    }
  }

  async function handleRemoveMember(userId: string, name: string) {
    if (!confirm(`Remove ${name} from this group?`)) return;
    await api.admin.groups.removeMember(id, userId);
    load();
  }

  async function handleGrantAccess(e: React.FormEvent) {
    e.preventDefault();
    setAccessError('');
    try {
      await api.admin.groups.grantAccess(id, accessForm.team_id, accessForm.role);
      setAccessForm({ team_id: '', role: 'viewer' });
      setAddingAccess(false);
      load();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed';
      try { setAccessError(JSON.parse(msg).error ?? msg); } catch { setAccessError(msg); }
    }
  }

  async function handleRevokeAccess(teamId: string, teamName: string) {
    if (!confirm(`Remove access to "${teamName}"?`)) return;
    await api.admin.groups.revokeAccess(id, teamId);
    load();
  }

  if (loading) return <div className="text-gray-500 p-4">Loading…</div>;
  if (!group) return <div className="text-gray-500 p-4">Group not found.</div>;

  const memberIds = new Set(members.map(m => m.id));
  const accessTeamIds = new Set(access.map(a => a.team_id));
  const availableUsers = allUsers.filter(u => !memberIds.has(u.id));
  const availableTeams = allTeams.filter(t => !accessTeamIds.has(t.id));

  return (
    <div className="max-w-3xl">
      <Link href="/admin/groups" className="text-sm text-blue-600 hover:underline mb-4 inline-block">
        ← Back to Groups
      </Link>

      {/* Header */}
      <div className="bg-white border border-gray-200 rounded-lg p-6 mb-6">
        <h1 className="text-xl font-bold text-gray-900">{group.name}</h1>
        {group.description && <p className="text-sm text-gray-500 mt-1">{group.description}</p>}
      </div>

      {/* Tabs */}
      <div className="flex border-b border-gray-200 mb-6">
        {(['members', 'access'] as Tab[]).map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-5 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors ${
              tab === t
                ? 'border-blue-600 text-blue-600'
                : 'border-transparent text-gray-500 hover:text-gray-700'
            }`}
          >
            {t === 'members' ? `Members (${members.length})` : `Team Access (${access.length})`}
          </button>
        ))}
      </div>

      {/* Members tab */}
      {tab === 'members' && (
        <div className="bg-white border border-gray-200 rounded-lg p-6">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-sm font-semibold text-gray-700">Group Members</h2>
            <button onClick={() => setAddingMember(!addingMember)}
              disabled={availableUsers.length === 0}
              className="text-sm px-3 py-1.5 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-40">
              {addingMember ? 'Cancel' : '+ Add Member'}
            </button>
          </div>

          {addingMember && (
            <form onSubmit={handleAddMember} className="flex gap-3 items-end mb-4 p-3 bg-gray-50 rounded-lg border border-gray-200">
              <div className="flex-1">
                <label className="block text-xs font-medium text-gray-600 mb-1">User</label>
                <select value={selectedUser} onChange={e => setSelectedUser(e.target.value)}
                  required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm bg-white">
                  <option value="">Select user…</option>
                  {availableUsers.map(u => (
                    <option key={u.id} value={u.id}>{u.name} ({u.username})</option>
                  ))}
                </select>
                {memberError && <p className="text-xs text-red-600 mt-1">{memberError}</p>}
              </div>
              <button type="submit" className="px-4 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700">
                Add
              </button>
            </form>
          )}

          {members.length === 0 ? (
            <p className="text-sm text-gray-400 text-center py-6">No members yet.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b border-gray-100">
                  <th className="pb-2 font-medium">Name</th>
                  <th className="pb-2 font-medium">Username</th>
                  <th className="pb-2 font-medium">Email</th>
                  <th className="pb-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-50">
                {members.map(m => (
                  <tr key={m.id}>
                    <td className="py-2.5 pr-4 font-medium text-gray-900">{m.name}</td>
                    <td className="py-2.5 pr-4 font-mono text-xs text-gray-600">{m.username}</td>
                    <td className="py-2.5 pr-4 text-gray-500">{m.email}</td>
                    <td className="py-2.5 text-right">
                      <button onClick={() => handleRemoveMember(m.id, m.name)}
                        className="text-xs text-red-500 hover:text-red-700">Remove</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {/* Team Access tab */}
      {tab === 'access' && (
        <div className="bg-white border border-gray-200 rounded-lg p-6">
          <div className="flex items-center justify-between mb-1">
            <h2 className="text-sm font-semibold text-gray-700">Team Access</h2>
            <button onClick={() => setAddingAccess(!addingAccess)}
              disabled={availableTeams.length === 0}
              className="text-sm px-3 py-1.5 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-40">
              {addingAccess ? 'Cancel' : '+ Grant Access'}
            </button>
          </div>
          <p className="text-xs text-gray-500 mb-4">
            Members of <strong>{group.name}</strong> will get these roles on login.
          </p>

          {addingAccess && (
            <form onSubmit={handleGrantAccess} className="flex gap-3 items-end mb-4 p-3 bg-gray-50 rounded-lg border border-gray-200">
              <div className="flex-1">
                <label className="block text-xs font-medium text-gray-600 mb-1">Team</label>
                <select value={accessForm.team_id} onChange={e => setAccessForm({ ...accessForm, team_id: e.target.value })}
                  required className="w-full px-3 py-1.5 border border-gray-300 rounded text-sm bg-white">
                  <option value="">Select team…</option>
                  {availableTeams.map(t => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-600 mb-1">Role</label>
                <select value={accessForm.role} onChange={e => setAccessForm({ ...accessForm, role: e.target.value })}
                  className="px-3 py-1.5 border border-gray-300 rounded text-sm bg-white">
                  <option value="viewer">Viewer</option>
                  <option value="responder">Responder</option>
                  <option value="admin">Admin</option>
                </select>
              </div>
              {accessError && <p className="text-xs text-red-600 mt-1">{accessError}</p>}
              <button type="submit" className="px-4 py-1.5 bg-blue-600 text-white text-sm rounded hover:bg-blue-700">
                Grant
              </button>
            </form>
          )}

          {access.length === 0 ? (
            <p className="text-sm text-gray-400 text-center py-6">
              No team access granted yet.<br />
              <span className="text-xs">Members of this group will have no team access until you add some.</span>
            </p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-gray-500 border-b border-gray-100">
                  <th className="pb-2 font-medium">Team</th>
                  <th className="pb-2 font-medium">Role</th>
                  <th className="pb-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-50">
                {access.map(a => (
                  <tr key={a.team_id}>
                    <td className="py-2.5 pr-4 font-medium text-gray-900">{a.team_name}</td>
                    <td className="py-2.5 pr-4">
                      <span className={`px-2 py-0.5 rounded text-xs font-medium ${
                        a.role === 'admin'     ? 'bg-red-100 text-red-700' :
                        a.role === 'responder' ? 'bg-blue-100 text-blue-700' :
                                                'bg-gray-100 text-gray-600'
                      }`}>{a.role}</span>
                    </td>
                    <td className="py-2.5 text-right">
                      <button onClick={() => handleRevokeAccess(a.team_id, a.team_name)}
                        className="text-xs text-red-500 hover:text-red-700">Revoke</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
