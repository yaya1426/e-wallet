import { useState, type ReactNode } from 'react';
import { NavLink, Outlet } from 'react-router';
import { useWallet } from '@/api/queries';
import { useAuth } from '@/auth/AuthProvider';
import { DisabledAction } from './DisabledAction';
import { en } from '@/i18n/en';

const links = [
  { to: '/', label: en.nav.home, end: true, moneyOut: false },
  { to: '/history', label: en.nav.history, end: false, moneyOut: false },
  { to: '/send', label: en.nav.send, end: false, moneyOut: true },
  { to: '/bank-account', label: en.nav.bankAccount, end: false, moneyOut: false },
];

/** Signed-in layout: header with nav (bottom tab bar on phones) and the page. */
export function AppShell() {
  const { session } = useAuth();
  const [signingOut, setSigningOut] = useState(false);
  const signOut = () => {
    setSigningOut(true);
    void session.logout();
  };
  return (
    <div className="min-h-screen pb-20 sm:pb-0">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-3xl items-center justify-between gap-4 px-4 py-3">
          <span className="text-lg font-bold tracking-tight text-brand-600">{en.appName}</span>
          <nav aria-label={en.nav.label} className="hidden sm:block">
            <NavItems />
          </nav>
          <button
            type="button"
            onClick={signOut}
            disabled={signingOut}
            className="rounded-md px-2 py-1 text-sm font-medium text-slate-700 hover:bg-slate-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600 disabled:opacity-60"
          >
            {en.home.signOut}
          </button>
        </div>
      </header>
      <main className="mx-auto max-w-3xl px-4 py-6">
        <Outlet />
      </main>
      <nav
        aria-label={`${en.nav.label} (mobile)`}
        className="fixed inset-x-0 bottom-0 border-t border-slate-200 bg-white sm:hidden"
      >
        <NavItems mobile />
      </nav>
    </div>
  );
}

function NavItems({ mobile = false }: { mobile?: boolean }) {
  // Send is disabled on a frozen wallet everywhere it appears (UI-BR-14).
  const frozen = useWallet().data?.status === 'FROZEN';
  const base = `block rounded-md px-3 py-2 text-center text-sm font-medium focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600 ${mobile ? 'px-1 py-3 text-xs' : ''}`;
  return (
    <ul className={mobile ? 'grid grid-cols-4' : 'flex gap-1'}>
      {links.map((l) => (
        <li key={l.to}>
          {frozen && l.moneyOut ? (
            <DisabledAction label={l.label} className={`${base} w-full cursor-not-allowed text-slate-400`} />
          ) : (
          <NavLink
            to={l.to}
            end={l.end}
            className={({ isActive }) =>
              // Active is shown by weight and underline too, not by colour only.
              `${base} ${isActive ? 'font-semibold text-brand-600 underline decoration-2 underline-offset-8' : 'text-slate-600 hover:text-slate-900'}`
            }
          >
            {l.label}
          </NavLink>
          )}
        </li>
      ))}
    </ul>
  );
}

/** Page heading + content, used inside the shell. */
export function Page({ title, children, actions }: { title: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <section>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {actions}
      </div>
      {children}
    </section>
  );
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`rounded-2xl border border-slate-200 bg-white p-4 shadow-sm sm:p-5 ${className}`}>{children}</div>;
}
