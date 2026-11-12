import { useEffect, useId, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { en } from '@/i18n/en';
import { Button } from './Button';

interface Props {
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
  /** While true both buttons are disabled and Escape does nothing (the request is in flight). */
  busy?: boolean;
  /**
   * Where focus goes when the dialog closes. By default the element focused before it opened;
   * pass a function when that element will be disabled or gone (e.g. after a submit).
   */
  returnFocus?: () => HTMLElement | null | undefined;
}

/**
 * Accessible modal confirmation. It is rendered at the end of <body>; everything else is made
 * `inert` while it is open, so Tab and clicks cannot leave it. Escape = Back.
 */
export function ConfirmDialog({ title, children, confirmLabel = en.dialog.confirm, onConfirm, onCancel, busy = false, returnFocus }: Props) {
  const titleId = useId();
  const bodyId = useId();
  const panel = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const latest = useRef({ onCancel, busy, returnFocus });
  useEffect(() => {
    latest.current = { onCancel, busy, returnFocus };
  });

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const host = panel.current?.closest('[data-dialog-host]');
    const others = [...document.body.children].filter((el): el is HTMLElement => el !== host && el instanceof HTMLElement && !el.inert);
    others.forEach((el) => (el.inert = true));
    confirmRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !latest.current.busy) {
        e.preventDefault();
        latest.current.onCancel();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      others.forEach((el) => (el.inert = false));
      const target = latest.current.returnFocus?.() ?? previous;
      // Wait a frame: the target may be re-enabled by the same update that closes the dialog.
      setTimeout(() => target?.focus?.(), 0);
    };
  }, []);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== 'Tab' || !panel.current) return;
    const focusable = [...panel.current.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input, textarea, [tabindex]:not([tabindex="-1"])')];
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (!first || !last) return;
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };

  return createPortal(
    <div data-dialog-host="" className="fixed inset-0 z-20 flex items-end justify-center bg-slate-900/40 p-4 sm:items-center">
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={bodyId}
        onKeyDown={onKeyDown}
        className="w-full max-w-md rounded-2xl bg-white p-6 shadow-xl"
      >
        <h2 id={titleId} className="mb-3 text-lg font-semibold">
          {title}
        </h2>
        <div id={bodyId} className="mb-6 space-y-2 text-sm text-slate-700">
          {children}
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row">
          <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
            {en.dialog.back}
          </Button>
          <Button ref={confirmRef} type="button" onClick={onConfirm} loading={busy}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
