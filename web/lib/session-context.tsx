'use client';

import { createContext, useContext, useEffect, useState, ReactNode } from 'react';
import { api } from './api';

interface Session {
  identityId: string;
  email: string;
  name: string;
  avatarUrl?: string;
  teamIds: string[];
  roles: Record<string, string>;
  authType: 'local' | 'sso' | '';
  isSuperAdmin: boolean;
  forcePasswordChange: boolean;
}

interface SessionContextValue {
  session: Session | null;
  loading: boolean;
  // Convenience: first team the user belongs to
  primaryTeamId: string | null;
}

const SessionContext = createContext<SessionContextValue>({
  session: null,
  loading: true,
  primaryTeamId: null,
});

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.auth.me()
      .then((data) => {
        setSession({
          identityId: data.identity_id,
          email: data.email,
          name: data.name,
          avatarUrl: data.avatar_url,
          teamIds: data.team_ids ?? [],
          roles: data.roles ?? {},
          authType: (data.auth_type as Session['authType']) ?? '',
          isSuperAdmin: data.is_superadmin ?? false,
          forcePasswordChange: data.force_password_change ?? false,
        });
      })
      .catch(() => setSession(null))
      .finally(() => setLoading(false));
  }, []);

  const primaryTeamId = session?.teamIds?.[0] ?? null;

  return (
    <SessionContext.Provider value={{ session, loading, primaryTeamId }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession() {
  return useContext(SessionContext);
}
