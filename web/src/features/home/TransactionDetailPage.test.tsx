import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { tx, wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';
import type { TransactionItem } from '@/lib/transactions';

const ID = '0193a0b2-0000-7000-8000-0000000000aa';

function serverWith(item: TransactionItem | 'missing') {
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, wallet()))
    .on(`GET /v1/transactions/${ID}`, () =>
      item === 'missing' ? apiError(404, 'TRANSACTION_NOT_FOUND') : json(200, item),
    );
}

describe('TransactionDetailPage (UI-BR-18)', () => {
  it('failed withdrawal shows the bank reason and the refund line (AC-6)', async () => {
    const item = tx({ id: ID, type: 'WITHDRAWAL', direction: 'OUT', status: 'FAILED', failure_code: 'ACCOUNT_CLOSED', amount_minor: 20000, completed_at: null });
    await renderApp(`/transactions/${ID}`, { server: serverWith(item), signedIn: true });
    expect(await screen.findByText(/The bank account is closed\./)).toHaveTextContent(
      'The bank account is closed. The money is back in your wallet.',
    );
    expect(screen.getByTestId('detail-amount').querySelector('s')).toHaveTextContent('−EGP 200.00');
    expect(screen.queryByText('Completed')).not.toBeInTheDocument();
  });

  it('unknown failure code shows the generic text with the code (AC-6)', async () => {
    const item = tx({ id: ID, status: 'FAILED', failure_code: 'BANK_DOWN' });
    await renderApp(`/transactions/${ID}`, { server: serverWith(item), signedIn: true });
    expect(await screen.findByText('The bank rejected this request. (BANK_DOWN)')).toBeInTheDocument();
  });

  it('P2P shows the other user, the note and Cairo times', async () => {
    const item = tx({
      id: ID,
      type: 'P2P',
      direction: 'IN',
      counterparty: { phone: '+201001234567', masked_name: 'Ahmed M. A.' },
      note: 'lunch',
      created_at: '2026-11-12T12:05:00Z',
      completed_at: '2026-11-12T12:05:00Z',
    });
    await renderApp(`/transactions/${ID}`, { server: serverWith(item), signedIn: true });
    expect(await screen.findByText('Received from Ahmed M. A.')).toBeInTheDocument();
    expect(screen.getByText('Ahmed M. A. (+20 100 123 4567)')).toBeInTheDocument();
    expect(screen.getByText('lunch')).toBeInTheDocument();
    expect(screen.getByText('Transfer · Money in')).toBeInTheDocument();
    expect(screen.getAllByText('12 Nov 2026, 14:05')).toHaveLength(2);
    expect(screen.getByTestId('detail-amount')).toHaveTextContent('+EGP 500.00');
  });

  it('404 shows "Transaction not found." with a link to history (AC-7)', async () => {
    await renderApp(`/transactions/${ID}`, { server: serverWith('missing'), signedIn: true });
    expect(await screen.findByText('Transaction not found.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Back to history' })).toHaveAttribute('href', '/history');
  });
});
