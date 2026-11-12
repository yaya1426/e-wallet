// Follows a top-up or withdrawal after 202 until the bank answers (PRD-005 UI-BR-25).
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { queryKeys, useTransaction, useWallet } from '@/api/queries';
import { en } from '@/i18n/en';
import { formatMoney } from '@/lib/money';
import { failureMessage, isPending, type TransactionItem } from '@/lib/transactions';
import { Alert } from './Alert';
import { Spinner } from './Spinner';

export const STATUS_POLL_MS = 2_000;
export const STATUS_POLL_FOR_MS = 60_000;

interface Props {
  transactionId: string;
  doneTitle: string;
}

export function TransactionStatusPanel({ transactionId, doneTitle }: Props) {
  const tx = useTransaction(transactionId, { pollEveryMs: STATUS_POLL_MS, pollForMs: STATUS_POLL_FOR_MS });
  const wallet = useWallet();
  const queryClient = useQueryClient();
  const [slow, setSlow] = useState(false);
  const region = useRef<HTMLDivElement>(null);

  // Focus lands here after the confirm dialog closes (keyboard and screen-reader users).
  useEffect(() => {
    region.current?.focus();
  }, []);

  // Once the answer is final, refetch the balance, whether or not we saw it pending first.
  const final = tx.data !== undefined && !isPending(tx.data.status);
  useEffect(() => {
    if (final) void queryClient.invalidateQueries({ queryKey: queryKeys.wallet });
  }, [final, queryClient]);

  useEffect(() => {
    const t = setTimeout(() => setSlow(true), STATUS_POLL_FOR_MS);
    return () => clearTimeout(t);
  }, [transactionId]);

  return (
    <div ref={region} tabIndex={-1} className="space-y-3 focus:outline-none" data-testid="tx-status">
      <StatusBody data={tx.data} slow={slow} doneTitle={doneTitle} balanceMinor={wallet.data?.available_balance_minor} />
      <Link to={`/transactions/${transactionId}`} className="block font-medium text-brand-600 hover:underline">
        {en.money.viewTransaction}
      </Link>
    </div>
  );
}

interface BodyProps {
  data: TransactionItem | undefined;
  slow: boolean;
  doneTitle: string;
  balanceMinor: number | undefined;
}

function StatusBody({ data, slow, doneTitle, balanceMinor }: BodyProps) {
  if (!data || isPending(data.status)) {
    return slow ? (
      <Alert tone="info">{en.money.slow}</Alert>
    ) : (
      <p role="status" className="flex items-center gap-2 text-slate-700">
        <Spinner small decorative />
        {en.money.waiting}
      </p>
    );
  }
  if (data.status === 'COMPLETED') {
    return (
      <>
        <Alert tone="success">{doneTitle}</Alert>
        {balanceMinor !== undefined && <p className="text-slate-700">{en.money.newBalance(formatMoney(balanceMinor))}</p>}
      </>
    );
  }
  return (
    <Alert tone="error">
      {failureMessage(data.failure_code)}
      {data.type === 'WITHDRAWAL' && <> {en.transactions.refunded}</>}
    </Alert>
  );
}
