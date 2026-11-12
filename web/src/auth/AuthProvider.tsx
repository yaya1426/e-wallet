import { createContext, useCallback, useContext, useEffect, useSyncExternalStore, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { ApiClient } from '@/api/client';
import type { Session, SessionState } from './session';

interface AuthContextValue {
  session: Session;
  api: ApiClient;
  state: SessionState;
}

const AuthContext = createContext<AuthContextValue | null>(null);

/** Provides the session and the API client bound to it (both injectable for tests). */
export function AuthProvider({ session, api, children }: { session: Session; api: ApiClient; children: ReactNode }) {
  const subscribe = useCallback((cb: () => void) => session.subscribe(cb), [session]);
  const getSnapshot = useCallback(() => session.getState(), [session]);
  const state = useSyncExternalStore(subscribe, getSnapshot);
  const queryClient = useQueryClient();
  useEffect(() => {
    // Nothing cached for one user may be shown after the session ends (TDR-020 §3).
    if (state.status === 'anonymous') queryClient.clear();
  }, [state.status, queryClient]);
  return <AuthContext.Provider value={{ session, api, state }}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>');
  return ctx;
}

export function useApi(): ApiClient {
  return useAuth().api;
}
