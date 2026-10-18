import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, whenSignedOut } from '@/lib/api';
import type { AuthState, Prefs, Principal } from '@/lib/types';
import { LoginPage } from './login';
import { RenewPage } from './renew';
import { AuthCard, Splash } from './splash';

// Who is at the console. People sign in with a name and a password: the
// console starts with admin, password admin, and whoever signs in with a
// password given them chooses one of their own before anything else.

interface Auth {
  state: AuthState;
  me: Principal;
  /** What to call the person. */
  who: string;
  prefs: Prefs;
  setPrefs: (next: Partial<Prefs>) => void;
  signOut: () => Promise<void>;
}

const AuthContext = createContext<Auth | null>(null);

const localPrefsKey = 'qoqi-prefs';

function localPrefs(): Prefs {
  try {
    return JSON.parse(localStorage.getItem(localPrefsKey) ?? '{}') as Prefs;
  } catch {
    return {};
  }
}

export function AuthGate({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const state = useQuery({ queryKey: ['auth'], queryFn: api.auth, staleTime: Infinity, retry: 2 });

  useEffect(() => {
    whenSignedOut(() => client.invalidateQueries({ queryKey: ['auth'] }));
  }, [client]);

  const [prefs, setPrefsState] = useState<Prefs>(localPrefs);
  useEffect(() => {
    if (state.data?.prefs) setPrefsState((p) => ({ ...p, ...state.data!.prefs }));
  }, [state.data?.prefs]);

  const setPrefs = useCallback(
    (next: Partial<Prefs>) => {
      setPrefsState((p) => {
        const merged = { ...p, ...next };
        try {
          localStorage.setItem(localPrefsKey, JSON.stringify(merged));
        } catch {
          // Kept for this visit only
        }
        if (state.data?.me?.user) api.prefs(merged as Record<string, unknown>).catch(() => undefined);
        return merged;
      });
    },
    [state.data?.me?.user],
  );

  const signOut = useCallback(async () => {
    await api.logout().catch(() => undefined);
    client.clear();
    location.assign('/');
  }, [client]);

  const value = useMemo<Auth | null>(() => {
    const me = state.data?.me;
    if (!me?.user || me.renew) return null;
    return { state: state.data!, me, who: me.user, prefs, setPrefs, signOut };
  }, [state.data, prefs, setPrefs, signOut]);

  if (state.isLoading) return <Splash />;
  if (state.isError || !state.data) return <Splash failed retry={() => state.refetch()} />;
  if (!state.data.accounts) {
    return <AuthCard title="This daemon has no console" hint="It was started without the console's tables: qoqi serve keeps them in its store." >{null}</AuthCard>;
  }
  if (state.data.me?.user && state.data.me.renew) return <RenewPage name={state.data.me.user} onSignOut={signOut} />;
  if (!value) return <LoginPage />;
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): Auth {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error('useAuth outside AuthGate');
  return auth;
}
