// Console frame: nav, Lock, and activity tracking for the idle lock (TDR-022 §3, §6).
import { NavLink, Outlet } from 'react-router';
import { en } from '@/i18n/en';
import { useOps } from './context';

const links = [
  { to: '/ops', label: en.ops.nav.health, end: true },
  { to: '/ops/discrepancies', label: en.ops.nav.discrepancies, end: false },
  { to: '/ops/wallets', label: en.ops.nav.wallets, end: false },
  { to: '/ops/reset-codes', label: en.ops.nav.resetCodes, end: false },
];

export function OpsLayout() {
  const { store } = useOps();
  const touch = () => store.touch();
  return (
    <div className="min-h-dvh bg-slate-50" onClickCapture={touch} onKeyDownCapture={touch}>
      <header className="border-b border-slate-800 bg-slate-900 text-white">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 px-4 py-3">
          <span className="font-semibold">{en.ops.title}</span>
          <button
            type="button"
            onClick={() => store.clear('locked')}
            className="rounded-md border border-white/40 px-3 py-1 text-sm font-medium hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-white"
          >
            {en.ops.lock}
          </button>
        </div>
        <nav aria-label={en.ops.nav.label} className="mx-auto max-w-5xl overflow-x-auto px-4">
          <ul className="flex gap-1">
            {links.map((l) => (
              <li key={l.to}>
                <NavLink
                  to={l.to}
                  end={l.end}
                  className={({ isActive }) =>
                    `block whitespace-nowrap border-b-2 px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-white ${
                      isActive ? 'border-white font-semibold underline underline-offset-4' : 'border-transparent text-slate-300 hover:text-white'
                    }`
                  }
                >
                  {l.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}

export function OpsNotFound() {
  return <p className="text-slate-700">{en.notFound.body}</p>;
}

/** Page heading + optional actions, like the user app's Page. */
export function OpsPage({ title, actions, children }: { title: string; actions?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {actions}
      </div>
      {children}
    </section>
  );
}
