// Display helpers for transactions (PRD-005 UI-BR-16, UI-BR-18).
import type { components } from '@/api/schema';
import { en } from '@/i18n/en';
import { formatMoney } from './money';

export type TransactionItem = components['schemas']['TransactionItem'];
export type TransactionStatus = TransactionItem['status'];

/** PENDING and PROCESSING both mean "the bank has not answered yet". */
export function isPending(status: TransactionStatus): boolean {
  return status === 'PENDING' || status === 'PROCESSING';
}

export function isFinal(status: TransactionStatus): boolean {
  return !isPending(status);
}

/** "Top-up", "Withdrawal", "Sent to Ahmed M. A.", "Received from Ahmed M. A." */
export function transactionTitle(item: Pick<TransactionItem, 'type' | 'direction' | 'counterparty'>): string {
  switch (item.type) {
    case 'TOPUP':
      return en.transactions.topUp;
    case 'WITHDRAWAL':
      return en.transactions.withdrawal;
    case 'P2P': {
      const name = item.counterparty?.masked_name ?? en.transactions.unknownUser;
      return item.direction === 'OUT' ? en.transactions.sentTo(name) : en.transactions.receivedFrom(name);
    }
  }
}

/** "+EGP 10.00" for money in, "−EGP 10.00" (U+2212) for money out. */
export function signedAmount(item: Pick<TransactionItem, 'direction' | 'amount_minor'>): string {
  return `${item.direction === 'IN' ? '+' : '−'}${formatMoney(item.amount_minor)}`;
}

export function typeLabel(type: TransactionItem['type']): string {
  return en.transactions.types[type];
}

export function statusLabel(status: TransactionStatus): string {
  if (isPending(status)) return en.transactions.status.pending;
  return status === 'COMPLETED' ? en.transactions.status.completed : en.transactions.status.failed;
}

/** The user-facing reason of a failed bank transaction (UI-BR-18). */
export function failureMessage(code: string | null | undefined): string {
  if (code && Object.hasOwn(en.bankFailures, code)) return en.bankFailures[code] ?? '';
  return code ? `${en.transactions.bankRejected} (${code})` : en.transactions.bankRejected;
}
