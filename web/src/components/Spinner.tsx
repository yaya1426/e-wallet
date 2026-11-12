import { en } from '@/i18n/en';

/** `decorative` hides it from assistive tech (e.g. inside a button that is already busy). */
export function Spinner({ small = false, decorative = false }: { small?: boolean; decorative?: boolean }) {
  const size = small ? 'h-4 w-4 border-2' : 'h-8 w-8 border-[3px]';
  return (
    <span
      role={decorative ? undefined : 'status'}
      aria-hidden={decorative || undefined}
      aria-label={decorative ? undefined : en.common.loading}
      className={`inline-block animate-spin rounded-full border-current border-r-transparent ${size}`}
    />
  );
}

export function FullPageSpinner() {
  return (
    <div className="flex min-h-screen items-center justify-center text-brand-600">
      <Spinner />
    </div>
  );
}
