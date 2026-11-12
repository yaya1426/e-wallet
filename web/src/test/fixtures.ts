// Sample API objects for component tests.
import type { TransactionItem } from '@/lib/transactions';

export const WALLET_ID = '0193a0b2-0000-7000-8000-000000000001';

export function wallet(over: Partial<{ available_balance_minor: number; status: 'ACTIVE' | 'FROZEN' }> = {}) {
  return { wallet_id: WALLET_ID, currency: 'EGP', available_balance_minor: 15075, status: 'ACTIVE', ...over };
}

let seq = 0;
export function tx(over: Partial<TransactionItem> = {}): TransactionItem {
  seq++;
  return {
    id: `0193a0b2-0000-7000-8000-${String(seq).padStart(12, '0')}`,
    type: 'TOPUP',
    status: 'COMPLETED',
    direction: 'IN',
    amount_minor: 50000,
    currency: 'EGP',
    counterparty: null,
    note: null,
    failure_code: null,
    failure_message: null,
    created_at: '2026-11-12T12:05:00Z',
    updated_at: '2026-11-12T12:05:00Z',
    completed_at: '2026-11-12T12:05:03Z',
    ...over,
  };
}

export function page(items: TransactionItem[], next_cursor: string | null = null) {
  return { items, next_cursor };
}
