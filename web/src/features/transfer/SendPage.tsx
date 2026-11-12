// Send money: recipient → amount and note → confirm → result (PRD-005 UI-BR-27..30, T033).
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { isApiError } from '@/api/errors';
import { invalidateMoney, queryKeys, useWallet } from '@/api/queries';
import { Alert } from '@/components/Alert';
import { Card, Page } from '@/components/AppShell';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { parseAmount } from '@/lib/money';
import { normalizeEgyptianMobile } from '@/lib/phone';
import { useIdempotentRequest } from '@/lib/useIdempotentRequest';
import { validateNote } from '@/lib/validation';
import { AmountStep } from './AmountStep';
import { ConfirmStep } from './ConfirmStep';
import { useRecipientPreview, useSendTransfer, type Recipient, type TransferVars } from './hooks';
import { RecipientStep } from './RecipientStep';
import { ResultView } from './ResultView';

type Step = 1 | 2 | 3;
type FocusTarget = 'heading' | 'phone' | 'amount' | 'note' | 'preview';

// Where a business error sends the user: the step that can fix it (T033 Q3).
const RECIPIENT_ERRORS = new Set(['RECIPIENT_NOT_FOUND', 'SELF_TRANSFER_NOT_ALLOWED', 'INVALID_PHONE']);
const AMOUNT_ERRORS = new Set(['INSUFFICIENT_FUNDS', 'DAILY_LIMIT_EXCEEDED', 'RECIPIENT_LIMIT_EXCEEDED', 'AMOUNT_OUT_OF_RANGE', 'INVALID_AMOUNT']);

function transferErrorMessage(err: unknown): string {
  if (isApiError(err) && err.code === 'AMOUNT_OUT_OF_RANGE') return en.money.outOfRange.transfer;
  return messageForError(err);
}

export function SendPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [round, setRound] = useState(0);
  return (
    <SendFlow
      key={round}
      prefill={round === 0 ? (params.get('to') ?? '').trim() : ''}
      onRestart={() => {
        if (params.has('to')) void navigate('/send', { replace: true });
        setRound((n) => n + 1);
      }}
    />
  );
}

function SendFlow({ prefill, onRestart }: { prefill: string; onRestart: () => void }) {
  const wallet = useWallet();
  const queryClient = useQueryClient();
  const preview = useRecipientPreview();
  const sendTransfer = useSendTransfer();

  const [step, setStep] = useState<Step>(1);
  const [focus, setFocus] = useState<{ target: FocusTarget; n: number } | null>(null);
  const [phone, setPhone] = useState(prefill);
  const [phoneError, setPhoneError] = useState<string | null>(null);
  const [recipient, setRecipient] = useState<Recipient | null>(null);
  const [amountText, setAmountText] = useState('');
  const [amountError, setAmountError] = useState<string | null>(null);
  const [note, setNote] = useState('');
  const [noteError, setNoteError] = useState<string | null>(null);
  const [vars, setVars] = useState<TransferVars | null>(null);

  const headingRef = useRef<HTMLHeadingElement>(null);
  const phoneRef = useRef<HTMLInputElement>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const noteRef = useRef<HTMLTextAreaElement>(null);
  const previewRef = useRef<HTMLDivElement>(null);

  const go = (next: Step, target: FocusTarget = 'heading') => {
    setStep(next);
    setFocus((f) => ({ target, n: (f?.n ?? 0) + 1 }));
  };

  // Move focus after a step change so keyboard and screen-reader users land on the new step.
  useEffect(() => {
    if (!focus) return;
    const el = { heading: headingRef, phone: phoneRef, amount: amountRef, note: noteRef, preview: previewRef }[focus.target].current;
    el?.focus();
  }, [focus]);

  const request = useIdempotentRequest('transfer', async (v: TransferVars, key: string) => {
    try {
      const transfer = await sendTransfer(v, key);
      void invalidateMoney(queryClient);
      return transfer;
    } catch (err) {
      if (isApiError(err)) {
        if (RECIPIENT_ERRORS.has(err.code)) {
          setRecipient(null);
          setPhoneError(messageForError(err));
          go(1, 'phone');
        } else if (AMOUNT_ERRORS.has(err.code)) {
          // The balance may have changed since step 2 (AC-6 race): show the current one.
          void queryClient.invalidateQueries({ queryKey: queryKeys.wallet });
          setAmountError(transferErrorMessage(err));
          go(2, 'amount');
        } else if (err.code === 'INVALID_NOTE') {
          setNoteError(messageForError(err));
          go(2, 'note');
        } else if (err.code === 'WALLET_FROZEN') {
          void queryClient.invalidateQueries({ queryKey: queryKeys.wallet });
        }
      }
      throw err;
    }
  });
  const state = request.state;

  // A completed transfer comes first: a later failed background refetch must never hide it.
  if (state.status === 'success') {
    return (
      <Page title={en.transfer.title}>
        <Card>
          <ResultView transfer={state.data} onSendAgain={onRestart} />
        </Card>
      </Page>
    );
  }

  if (wallet.isPending) {
    return (
      <Page title={en.transfer.title}>
        <Card>
          <Spinner small />
        </Card>
      </Page>
    );
  }
  if (wallet.data === undefined) {
    return (
      <Page title={en.transfer.title}>
        <Card>
          <ErrorWithRetry error={wallet.error} onRetry={() => void wallet.refetch()} />
        </Card>
      </Page>
    );
  }
  if (wallet.data.status === 'FROZEN') {
    return (
      <Page title={en.transfer.title}>
        <Alert tone="info">{en.home.frozen}</Alert>
      </Page>
    );
  }

  const checkRecipient = () => {
    const e164 = normalizeEgyptianMobile(phone);
    if (!e164) {
      setPhoneError(en.validation.phoneInvalid);
      phoneRef.current?.focus();
      return;
    }
    setPhoneError(null);
    preview.mutate(e164, {
      onSuccess: (r) => {
        setRecipient(r);
        // Land on the card so screen readers read out who will be paid.
        setFocus((f) => ({ target: 'preview', n: (f?.n ?? 0) + 1 }));
      },
      onError: (err) => {
        setPhoneError(messageForError(err));
        phoneRef.current?.focus();
      },
    });
  };

  const toConfirm = () => {
    const parsed = parseAmount(amountText);
    let amountMsg: string | null = null;
    if (!parsed.ok) amountMsg = en.amount.errors[parsed.error];
    else if (parsed.minor > wallet.data.available_balance_minor) amountMsg = en.transfer.notEnough;
    const noteKey = validateNote(note);
    setAmountError(amountMsg);
    setNoteError(noteKey ? en.validation[noteKey] : null);
    if (amountMsg || !parsed.ok) return amountRef.current?.focus();
    if (noteKey) return noteRef.current?.focus();
    if (!recipient) return go(1, 'phone');
    const trimmed = note.trim();
    setVars({ recipient_phone: recipient.phone, amount_minor: parsed.minor, ...(trimmed ? { note: trimmed } : {}) });
    request.reset();
    go(3);
  };

  const headings = { 1: en.transfer.recipientHeading, 2: en.transfer.amountHeading, 3: en.transfer.confirmHeading };
  const error = state.status === 'error' && step === 3 ? state : null;

  return (
    <Page title={en.transfer.title}>
      <Card>
        <p aria-live="polite" className="text-sm font-medium text-slate-500">
          {en.transfer.step(step)}
        </p>
        <h2 ref={headingRef} tabIndex={-1} className="mb-4 text-lg font-semibold focus:outline-none">
          {headings[step]}
        </h2>
        {step === 1 && (
          <RecipientStep
            phone={phone}
            onPhoneChange={(v) => {
              setPhone(v);
              setPhoneError(null);
            }}
            error={phoneError}
            recipient={recipient}
            checking={preview.isPending}
            onCheck={checkRecipient}
            onChange={() => {
              setRecipient(null);
              setFocus((f) => ({ target: 'phone', n: (f?.n ?? 0) + 1 }));
            }}
            onContinue={() => go(2, 'amount')}
            phoneRef={phoneRef}
            previewRef={previewRef}
          />
        )}
        {step === 2 && (
          <AmountStep
            amount={amountText}
            onAmountChange={(v) => {
              setAmountText(v);
              setAmountError(null);
            }}
            amountError={amountError}
            note={note}
            onNoteChange={(v) => {
              setNote(v);
              setNoteError(null);
            }}
            noteError={noteError}
            availableMinor={wallet.data.available_balance_minor}
            onBack={() => go(1)}
            onContinue={toConfirm}
            amountRef={amountRef}
            noteRef={noteRef}
          />
        )}
        {step === 3 && recipient && vars && (
          <ConfirmStep
            recipient={recipient}
            amountMinor={vars.amount_minor}
            note={vars.note ?? ''}
            sending={state.status === 'pending'}
            error={error}
            onBack={() => {
              request.reset();
              go(2, 'amount');
            }}
            onSend={() => void request.submit(vars)}
            onRetry={() => void request.retry()}
          />
        )}
      </Card>
    </Page>
  );
}
