import { useEffect, useState } from 'react';
import { en } from '@/i18n/en';

/** Copies `text` and says so in a live region; on failure tells the user to copy by hand. */
export function CopyButton({ text, label }: { text: string; label: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (state !== 'copied') return;
    const t = setTimeout(() => setState('idle'), 2000);
    return () => clearTimeout(t);
  }, [state]);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setState('copied');
    } catch {
      setState('failed');
    }
  };
  return (
    <span className="inline-flex items-center gap-2">
      <button
        type="button"
        onClick={() => void copy()}
        aria-label={`${en.copy.copy} ${label}`}
        className="rounded-md border border-slate-300 bg-white px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600"
      >
        {state === 'copied' ? en.copy.copied : en.copy.copy}
      </button>
      <span aria-live="polite" className="text-xs">
        {state === 'failed' && <span className="text-red-700">{en.copy.failed}</span>}
        {state === 'copied' && <span className="sr-only">{en.copy.copiedAnnounce}</span>}
      </span>
    </span>
  );
}
