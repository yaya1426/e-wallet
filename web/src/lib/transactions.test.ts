import { describe, expect, it } from 'vitest';
import { tx } from '@/test/fixtures';
import { failureMessage, isPending, signedAmount, statusLabel, transactionTitle } from './transactions';

const ahmed = { phone: '+201001234567', masked_name: 'Ahmed M. A.' };

describe('transaction helpers (UI-BR-16, UI-BR-18)', () => {
  it('titles', () => {
    expect(transactionTitle(tx({ type: 'TOPUP' }))).toBe('Top-up');
    expect(transactionTitle(tx({ type: 'WITHDRAWAL', direction: 'OUT' }))).toBe('Withdrawal');
    expect(transactionTitle(tx({ type: 'P2P', direction: 'OUT', counterparty: ahmed }))).toBe('Sent to Ahmed M. A.');
    expect(transactionTitle(tx({ type: 'P2P', direction: 'IN', counterparty: ahmed }))).toBe('Received from Ahmed M. A.');
    expect(transactionTitle(tx({ type: 'P2P', direction: 'IN', counterparty: null }))).toBe('Received from another user');
  });

  it('signs amounts with + and U+2212', () => {
    expect(signedAmount(tx({ direction: 'IN', amount_minor: 1050 }))).toBe('+EGP 10.50');
    expect(signedAmount(tx({ direction: 'OUT', amount_minor: 123456 }))).toBe('−EGP 1,234.56');
  });

  it('labels statuses; PROCESSING counts as pending', () => {
    expect(statusLabel('PENDING')).toBe('Pending');
    expect(statusLabel('PROCESSING')).toBe('Pending');
    expect(statusLabel('COMPLETED')).toBe('Completed');
    expect(statusLabel('FAILED')).toBe('Failed');
    expect(isPending('PROCESSING')).toBe(true);
    expect(isPending('FAILED')).toBe(false);
  });

  it('maps bank failure codes, unknown ones with the code', () => {
    expect(failureMessage('INSUFFICIENT_FUNDS')).toBe("Your bank account didn't have enough money.");
    expect(failureMessage('INVALID_ACCOUNT')).toBe('The bank account details are not valid.');
    expect(failureMessage('ACCOUNT_CLOSED')).toBe('The bank account is closed.');
    expect(failureMessage('WEIRD')).toBe('The bank rejected this request. (WEIRD)');
    expect(failureMessage(null)).toBe('The bank rejected this request.');
  });
});
