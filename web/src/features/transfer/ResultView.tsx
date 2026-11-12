// After 201: what was sent and the new balance (PRD-005 UI-BR-30).
import { useEffect, useRef } from 'react';
import { Link } from 'react-router';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { en } from '@/i18n/en';
import { formatMoney } from '@/lib/money';
import type { Transfer } from './hooks';

export function ResultView({ transfer, onSendAgain }: { transfer: Transfer; onSendAgain: () => void }) {
  const region = useRef<HTMLDivElement>(null);
  useEffect(() => region.current?.focus(), []);
  return (
    <div ref={region} tabIndex={-1} className="space-y-3 focus:outline-none" data-testid="transfer-result">
      <Alert tone="success">{en.transfer.sent(formatMoney(transfer.amount_minor), transfer.recipient.masked_name)}</Alert>
      <p className="text-slate-700">{en.money.newBalance(formatMoney(transfer.balance_after_minor))}</p>
      <Link to={`/transactions/${transfer.transaction_id}`} className="block font-medium text-brand-600 hover:underline">
        {en.money.viewTransaction}
      </Link>
      <div className="flex flex-col gap-2 pt-2 sm:flex-row">
        <Button type="button" variant="secondary" onClick={onSendAgain}>
          {en.transfer.sendAgain}
        </Button>
      </div>
      <Link to="/" className="block text-sm font-medium text-brand-600 hover:underline">
        {en.money.home}
      </Link>
    </div>
  );
}
