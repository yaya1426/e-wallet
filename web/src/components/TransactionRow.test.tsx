import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it } from 'vitest';
import { tx } from '@/test/fixtures';
import type { TransactionItem } from '@/lib/transactions';
import { TransactionList } from './TransactionRow';

const ahmed = { phone: '+201001234567', masked_name: 'Ahmed M. A.' };

function renderRow(item: TransactionItem) {
  const router = createMemoryRouter([{ path: '/', element: <TransactionList items={[item]} /> }]);
  render(<RouterProvider router={router} />);
  return screen.getByRole('link');
}

describe('TransactionRow (AC-4, UI-BR-16)', () => {
  it.each([
    ['top-up completed', tx({ type: 'TOPUP', direction: 'IN', status: 'COMPLETED', amount_minor: 50000 }), 'Top-up', '+EGP 500.00', 'Completed'],
    ['top-up processing', tx({ type: 'TOPUP', direction: 'IN', status: 'PROCESSING', amount_minor: 100 }), 'Top-up', '+EGP 1.00', 'Pending'],
    ['withdrawal pending', tx({ type: 'WITHDRAWAL', direction: 'OUT', status: 'PENDING', amount_minor: 20000 }), 'Withdrawal', '−EGP 200.00', 'Pending'],
    ['P2P sent', tx({ type: 'P2P', direction: 'OUT', counterparty: ahmed, amount_minor: 2500 }), 'Sent to Ahmed M. A.', '−EGP 25.00', 'Completed'],
    ['P2P received', tx({ type: 'P2P', direction: 'IN', counterparty: ahmed, amount_minor: 2500 }), 'Received from Ahmed M. A.', '+EGP 25.00', 'Completed'],
  ])('%s', (_name, item, title, amount, badge) => {
    const row = renderRow(item);
    expect(row).toHaveAttribute('href', `/transactions/${item.id}`);
    expect(row).toHaveTextContent(title);
    expect(screen.getByTestId('tx-amount')).toHaveTextContent(amount);
    expect(screen.getByTestId('tx-amount').querySelector('s')).toBeNull();
    expect(row).toHaveTextContent(badge);
    expect(row).toHaveTextContent('12 Nov 2026, 14:05');
  });

  it('failed withdrawal: amount struck through, "(no money moved)"', () => {
    renderRow(tx({ type: 'WITHDRAWAL', direction: 'OUT', status: 'FAILED', amount_minor: 20000 }));
    const amount = screen.getByTestId('tx-amount');
    expect(amount.querySelector('s')).toHaveTextContent('−EGP 200.00');
    expect(amount).toHaveTextContent('(no money moved)');
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });
});
