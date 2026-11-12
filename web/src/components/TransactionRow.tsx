import { Link } from 'react-router';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { signedAmount, transactionTitle, type TransactionItem } from '@/lib/transactions';
import { StatusBadge } from './StatusBadge';

/** One line of the history (PRD-005 UI-BR-16). The whole row links to the detail page. */
export function TransactionRow({ item }: { item: TransactionItem }) {
  const failed = item.status === 'FAILED';
  const amount = signedAmount(item);
  return (
    <li>
      <Link
        to={`/transactions/${item.id}`}
        className="flex items-center justify-between gap-3 rounded-lg px-3 py-3 hover:bg-slate-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600"
      >
        <span className="min-w-0">
          <span className="block truncate font-medium text-slate-900">{transactionTitle(item)}</span>
          <span className="block text-xs text-slate-500">{formatDateTime(item.created_at)}</span>
        </span>
        <span className="flex shrink-0 flex-col items-end gap-1">
          <span
            data-testid="tx-amount"
            className={`font-semibold tabular-nums ${failed ? 'text-slate-400' : item.direction === 'IN' ? 'text-emerald-700' : 'text-slate-900'}`}
          >
            {failed ? (
              <>
                <s>{amount}</s>
                <span className="sr-only"> {en.transactions.noMoneyMoved}</span>
              </>
            ) : (
              amount
            )}
          </span>
          <StatusBadge status={item.status} />
        </span>
      </Link>
    </li>
  );
}

export function TransactionList({ items }: { items: TransactionItem[] }) {
  return <ul className="divide-y divide-slate-100">{items.map((t) => <TransactionRow key={t.id} item={t} />)}</ul>;
}
