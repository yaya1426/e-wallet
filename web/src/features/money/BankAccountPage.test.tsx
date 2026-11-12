import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

const IBAN = 'EG890003000100000000123450000';
const account = { iban: IBAN, holder_name: 'Ahmed Ali', updated_at: '2026-11-12T12:05:00Z' };

function server(linked: boolean) {
  let current = linked ? account : null;
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, wallet()))
    .on('GET /v1/me/bank-account', () => (current ? json(200, current) : apiError(404, 'BANK_ACCOUNT_NOT_FOUND')))
    .on('PUT /v1/me/bank-account', (_req, body) => {
      const b = body as { iban: string; holder_name: string };
      current = { iban: b.iban.replaceAll(' ', '').toUpperCase(), holder_name: b.holder_name.trim(), updated_at: '2026-11-12T13:00:00Z' };
      return json(200, current);
    });
}

describe('BankAccountPage (T032)', () => {
  it('links a first account (AC-2)', async () => {
    const s = server(false);
    await renderApp('/bank-account', { server: s, signedIn: true });
    expect(await screen.findByText('No bank account linked yet.')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Link bank account' }));
    await user.type(screen.getByLabelText('IBAN'), 'eg89 0003 0001 0000 0000 1234 5000 0');
    await user.type(screen.getByLabelText('Account holder name'), 'Ahmed Ali');
    await user.click(screen.getByRole('button', { name: 'Save bank account' }));
    expect(await screen.findByText('Bank account saved.')).toBeInTheDocument();
    expect(screen.getByTestId('bank-account')).toHaveTextContent('EG89 0003 0001 0000 0000 1234 5000 0');
    expect(s.calls.find((c) => c.method === 'PUT')?.body).toEqual({ iban: 'eg89 0003 0001 0000 0000 1234 5000 0', holder_name: 'Ahmed Ali' });
  });

  it('invalid IBAN and short name: field errors and no request (AC-2)', async () => {
    const s = server(false);
    await renderApp('/bank-account', { server: s, signedIn: true });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Link bank account' }));
    await user.type(screen.getByLabelText('IBAN'), 'EG880003000100000000123450000');
    await user.type(screen.getByLabelText('Account holder name'), 'A');
    await user.click(screen.getByRole('button', { name: 'Save bank account' }));
    expect(screen.getByLabelText('IBAN')).toHaveAccessibleDescription(/Enter a valid Egyptian IBAN/);
    expect(screen.getByLabelText('Account holder name')).toHaveAccessibleDescription('Name must be 2 to 100 characters.');
    expect(screen.getByLabelText('IBAN')).toHaveFocus();
    expect(s.count('PUT /v1/me/bank-account')).toBe(0);
  });

  it('server INVALID_IBAN shows next to the field', async () => {
    const s = server(false).on('PUT /v1/me/bank-account', () => apiError(400, 'INVALID_IBAN'));
    await renderApp('/bank-account', { server: s, signedIn: true });
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Link bank account' }));
    await user.type(screen.getByLabelText('IBAN'), IBAN);
    await user.type(screen.getByLabelText('Account holder name'), 'Ahmed Ali');
    await user.click(screen.getByRole('button', { name: 'Save bank account' }));
    expect(await screen.findByText(/Enter a valid Egyptian IBAN/)).toBeInTheDocument();
  });

  it('replacing asks for confirmation; Back keeps the old account (AC-3)', async () => {
    const s = server(true);
    await renderApp('/bank-account', { server: s, signedIn: true });
    expect(await screen.findByTestId('bank-account')).toHaveTextContent('Ahmed Ali');
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Replace bank account' }));
    await user.type(screen.getByLabelText('IBAN'), 'EG620003000100000000123450001');
    await user.type(screen.getByLabelText('Account holder name'), 'Mona Saad');
    await user.click(screen.getByRole('button', { name: 'Save bank account' }));
    const dialog = screen.getByRole('dialog', { name: 'Replace your bank account?' });
    expect(within(dialog).getByRole('button', { name: 'Confirm' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(s.count('PUT /v1/me/bank-account')).toBe(0);
    await user.click(screen.getByRole('button', { name: 'Save bank account' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText('Bank account saved.')).toBeInTheDocument();
    expect(screen.getByTestId('bank-account')).toHaveTextContent('Mona Saad');
    expect(s.count('PUT /v1/me/bank-account')).toBe(1);
  });
});
