import type { ReactNode } from 'react';
import { en } from '@/i18n/en';

/** Centered card used by the auth screens and the placeholder home. */
export function PageLayout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col items-center px-4 py-10 sm:justify-center">
      <p className="mb-6 text-lg font-bold tracking-tight text-brand-600">{en.appName}</p>
      <main className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-sm sm:p-8">
        <h1 className="mb-6 text-2xl font-semibold tracking-tight">{title}</h1>
        {children}
      </main>
    </div>
  );
}
