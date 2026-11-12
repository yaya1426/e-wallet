import type { ReactNode } from 'react';
import { Navigate, useLocation, useSearchParams } from 'react-router';
import { FullPageSpinner } from '@/components/Spinner';
import { useAuth } from './AuthProvider';
import { safeNext } from './next';

/** Protected pages: anonymous visitors go to /login?next=<where they wanted to go>. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const location = useLocation();
  if (state.status === 'loading') return <FullPageSpinner />;
  if (state.status === 'anonymous') {
    // After an explicit sign-out there is nowhere to come back to.
    if (state.endReason === 'signedOut') return <Navigate to="/login" replace />;
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}`} replace />;
  }
  return <>{children}</>;
}

/** Auth pages: a signed-in user is sent on to `next` (or home). */
export function RedirectIfAuthed({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const [params] = useSearchParams();
  if (state.status === 'loading') return <FullPageSpinner />;
  if (state.status === 'authenticated') return <Navigate to={safeNext(params.get('next'))} replace />;
  return <>{children}</>;
}
