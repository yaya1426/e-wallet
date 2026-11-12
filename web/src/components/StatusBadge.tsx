import { statusLabel, type TransactionStatus } from '@/lib/transactions';

const looks: Record<'pending' | 'completed' | 'failed', string> = {
  pending: 'bg-amber-100 text-amber-800',
  completed: 'bg-emerald-100 text-emerald-800',
  failed: 'bg-red-100 text-red-800',
};

/** Text first, colour second (readable without colour, UI-NFR-2). */
export function StatusBadge({ status }: { status: TransactionStatus }) {
  const look = status === 'COMPLETED' ? 'completed' : status === 'FAILED' ? 'failed' : 'pending';
  return (
    <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${looks[look]}`}>
      {statusLabel(status)}
    </span>
  );
}
