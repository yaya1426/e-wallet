// Top-up and withdrawal: amount → confirm → submit → status (PRD-005 UI-BR-21..26, T032).
import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import { isApiError } from '@/api/errors';
import { invalidateMoney, queryKeys, useWallet } from '@/api/queries';
import { AmountField } from '@/components/AmountField';
import { Alert } from '@/components/Alert';
import { Card, Page } from '@/components/AppShell';
import { Button } from '@/components/Button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { TransactionStatusPanel } from '@/components/TransactionStatusPanel';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { ibanLast4 } from '@/lib/iban';
import { formatMoney, parseAmount } from '@/lib/money';
import { useIdempotentRequest } from '@/lib/useIdempotentRequest';
import { bankAccountKey, useBankAccount, useSendMoney, type MoneyKind } from './hooks';

export function TopUpPage() {
  return <MoneyFlowPage kind="topUp" />;
}

export function WithdrawPage() {
  return <MoneyFlowPage kind="withdrawal" />;
}

/** Message for a failed submit; AMOUNT_OUT_OF_RANGE names the flow (UI-BR-21). */
function submitErrorMessage(kind: MoneyKind, err: unknown): string {
  if (isApiError(err) && err.code === 'AMOUNT_OUT_OF_RANGE') return en.money.outOfRange[kind];
  return messageForError(err);
}

export function MoneyFlowPage({ kind }: { kind: MoneyKind }) {
  const text = en.money[kind];
  const wallet = useWallet();
  const account = useBankAccount();
  const queryClient = useQueryClient();
  const send = useSendMoney(kind);
  const request = useIdempotentRequest(kind, async (minor: number, key: string) => {
    try {
      const accepted = await send(minor, key);
      void invalidateMoney(queryClient); // the withdrawal reserve or the new pending item shows at once
      return accepted;
    } catch (err) {
      // The screen state behind the error changed: refetch it so the right message shows.
      if (isApiError(err) && err.code === 'WALLET_FROZEN') void queryClient.invalidateQueries({ queryKey: queryKeys.wallet });
      if (isApiError(err) && err.code === 'BANK_ACCOUNT_REQUIRED') void queryClient.invalidateQueries({ queryKey: bankAccountKey });
      throw err;
    }
  });

  const [amountText, setAmountText] = useState('');
  const [amountError, setAmountError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<number | null>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const state = request.state;

  // After 202: follow the transaction (UI-BR-25). This comes first: a later failed background
  // refetch must never hide an accepted request (the user might send it again).
  if (state.status === 'success') {
    return (
      <Page title={text.title}>
        <Card>
          <TransactionStatusPanel transactionId={state.data.transaction_id} doneTitle={text.done} />
          <div className="mt-4 flex flex-col gap-2 sm:flex-row">
            <Link to="/" className="text-sm font-medium text-brand-600 hover:underline">
              {en.money.home}
            </Link>
          </div>
        </Card>
      </Page>
    );
  }

  if (wallet.isPending || account.isPending) {
    return (
      <Page title={text.title}>
        <Card>
          <Spinner small />
        </Card>
      </Page>
    );
  }
  // Only a failed first load blocks the form; a failed background refetch keeps the old data.
  const loadError = (wallet.data === undefined && wallet.error) || (account.data === undefined && account.error);
  if (loadError) {
    return (
      <Page title={text.title}>
        <Card>
          <ErrorWithRetry
            error={loadError}
            onRetry={() => {
              void wallet.refetch();
              void account.refetch();
            }}
          />
        </Card>
      </Page>
    );
  }

  if (wallet.data?.status === 'FROZEN') {
    return (
      <Page title={text.title}>
        <Alert tone="info">{en.home.frozen}</Alert>
      </Page>
    );
  }

  if (!account.data) {
    return (
      <Page title={text.title}>
        <Card>
          <p className="mb-3 text-slate-700">{en.bank.linkFirst}</p>
          <Link to="/bank-account" className="font-medium text-brand-600 hover:underline">
            {en.bank.goLink}
          </Link>
        </Card>
      </Page>
    );
  }

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    const parsed = parseAmount(amountText);
    if (!parsed.ok) {
      setAmountError(en.amount.errors[parsed.error]);
      amountRef.current?.focus();
      return;
    }
    setAmountError(null);
    request.reset();
    setConfirming(parsed.minor);
  };

  const pending = state.status === 'pending';
  const error = state.status === 'error' ? state : null;
  const last4 = ibanLast4(account.data.iban);

  return (
    <Page title={text.title}>
      <Card>
        <p className="mb-4 text-slate-600">{text.intro}</p>
        <form noValidate onSubmit={onSubmit} className="space-y-4">
          <AmountField
            ref={amountRef}
            value={amountText}
            onChange={(e) => {
              setAmountText(e.target.value);
              request.reset(); // hide an old error and its "Try again" while editing
            }}
            error={amountError}
            disabled={pending}
          />
          {kind === 'withdrawal' && wallet.data && (
            <p className="text-sm text-slate-600">{en.amount.available(formatMoney(wallet.data.available_balance_minor))}</p>
          )}
          {error && (
            <div ref={resultRef} tabIndex={-1} className="space-y-2 focus:outline-none">
              <Alert tone="error">{submitErrorMessage(kind, error.error)}</Alert>
              {error.retryable && (
                <Button type="button" variant="secondary" onClick={() => void request.retry()}>
                  {en.common.retry}
                </Button>
              )}
            </div>
          )}
          <Button type="submit" loading={pending}>
            {text.submit}
          </Button>
        </form>
      </Card>
      {confirming !== null && (
        <ConfirmDialog
          title={text.confirmTitle}
          busy={pending}
          onCancel={() => {
            setConfirming(null);
            request.reset();
          }}
          returnFocus={() => resultRef.current}
          onConfirm={() => {
            // The dialog stays open (busy) until the answer, so a double click hits a disabled button.
            void request.submit(confirming).finally(() => setConfirming(null));
          }}
        >
          <p>{text.confirm(formatMoney(confirming), last4)}</p>
          {kind === 'withdrawal' && <p>{en.money.withdrawal.reserveNote}</p>}
        </ConfirmDialog>
      )}
    </Page>
  );
}
