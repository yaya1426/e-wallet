// Step 3: summary, then Send (PRD-005 UI-BR-29).
import { useEffect, useRef } from 'react';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { formatMoney } from '@/lib/money';
import { formatPhone } from '@/lib/phone';
import type { Recipient } from './hooks';

interface Props {
  recipient: Recipient;
  amountMinor: number;
  note: string;
  sending: boolean;
  error: { error: unknown; retryable: boolean } | null;
  onBack: () => void;
  onSend: () => void;
  onRetry: () => void;
}

export function ConfirmStep({ recipient, amountMinor, note, sending, error, onBack, onSend, onRetry }: Props) {
  // Send is replaced by Try again after a retryable failure: keep keyboard users on the action.
  const retryRef = useRef<HTMLButtonElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!error) return;
    if (error.retryable) retryRef.current?.focus();
    else errorRef.current?.focus();
  }, [error]);
  return (
    <div className="space-y-4">
      <dl className="divide-y divide-slate-100 rounded-xl border border-slate-200">
        <Row label={en.transfer.summary.to}>
          <span className="font-medium">{recipient.masked_name}</span>{' '}
          <span className="text-slate-600 tabular-nums">({formatPhone(recipient.phone)})</span>
        </Row>
        <Row label={en.transfer.summary.amount}>
          <span className="font-semibold tabular-nums">{formatMoney(amountMinor)}</span>
        </Row>
        {note && (
          <Row label={en.transfer.summary.note}>
            <span className="break-words">{note}</span>
          </Row>
        )}
      </dl>
      {error && (
        <div ref={errorRef} tabIndex={-1} className="space-y-2 focus:outline-none">
          <Alert tone="error">{messageForError(error.error)}</Alert>
          {error.retryable && (
            <Button ref={retryRef} type="button" variant="secondary" onClick={onRetry} loading={sending}>
              {en.common.retry}
            </Button>
          )}
        </div>
      )}
      <div className="flex flex-col-reverse gap-2 sm:flex-row">
        <Button type="button" variant="secondary" onClick={onBack} disabled={sending}>
          {en.transfer.back}
        </Button>
        {!error?.retryable && (
          <Button type="button" onClick={onSend} loading={sending}>
            {en.transfer.send}
          </Button>
        )}
      </div>
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 px-4 py-3 sm:flex-row sm:gap-4">
      <dt className="w-24 shrink-0 text-sm text-slate-500">{label}</dt>
      <dd className="min-w-0 text-slate-900">{children}</dd>
    </div>
  );
}
