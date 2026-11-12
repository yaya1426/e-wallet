import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { tx, wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

const TX = '0193a0b2-0000-7000-8000-0000000000f1';
const account = { iban: 'EG890003000100000000123450000', holder_name: 'Ahmed Ali', updated_at: '2026-11-12T12:05:00Z' };

afterEach(() => vi.useRealTimers());

function accepted(type: 'TOPUP' | 'WITHDRAWAL', amount: number) {
  return json(202, { transaction_id: TX, type, status: 'PENDING', amount_minor: amount, currency: 'EGP', created_at: '2026-11-12T12:05:00Z' });
}

function server(opts: { linked?: boolean; status?: 'ACTIVE' | 'FROZEN'; balance?: number } = {}) {
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, wallet({ status: opts.status ?? 'ACTIVE', available_balance_minor: opts.balance ?? 50000 })))
    .on('GET /v1/me/bank-account', () => (opts.linked === false ? apiError(404, 'BANK_ACCOUNT_NOT_FOUND') : json(200, account)))
    .on('GET /v1/me/transactions', () => json(200, { items: [], next_cursor: null }));
}

async function enterAndConfirm(amount: string) {
  const user = userEvent.setup();
  const field = await screen.findByLabelText('Amount');
  await user.clear(field);
  await user.type(field, amount);
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  return user;
}

const keys = (s: FakeServer, route: string) =>
  s.fetch.mock.calls.map(([r]) => r as Request).filter((r) => `${r.method} ${new URL(r.url).pathname}` === route).map((r) => r.headers.get('Idempotency-Key'));

describe('Top-up / withdrawal flow (T032)', () => {
  it('no linked account: "Link a bank account first." (AC-4)', async () => {
    await renderApp('/top-up', { server: server({ linked: false }), signedIn: true });
    expect(await screen.findByText('Link a bank account first.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Link a bank account' })).toHaveAttribute('href', '/bank-account');
  });

  it('frozen wallet shows the banner instead of the form (AC-12)', async () => {
    await renderApp('/withdraw', { server: server({ status: 'FROZEN' }), signedIn: true });
    expect(await screen.findByText(/Your wallet is frozen/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Amount')).not.toBeInTheDocument();
  });

  it.each([
    ['abc', 'Enter an amount like 250 or 99.50.'],
    ['1.234', 'Use at most 2 decimal places.'],
    ['0', 'The amount must be more than zero.'],
    ['', 'Enter an amount.'],
  ])('amount %j is rejected in the form (AC-9)', async (input, message) => {
    const s = server();
    await renderApp('/top-up', { server: s, signedIn: true });
    if (input) await enterAndConfirm(input);
    else await userEvent.setup().click(await screen.findByRole('button', { name: 'Continue' }));
    expect(screen.getByLabelText('Amount')).toHaveAccessibleDescription(message);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('top-up: confirm, send piasters with a key, then follow the status to completion (AC-9, AC-5)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let status: 'PENDING' | 'COMPLETED' = 'PENDING';
    let balance = 0;
    const s = server()
      .on('GET /v1/me/wallet', () => json(200, wallet({ available_balance_minor: balance })))
      .on('POST /v1/topups', () => accepted('TOPUP', 125050))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status })));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('1,250.5');
    const dialog = screen.getByRole('dialog', { name: 'Confirm top-up' });
    expect(dialog).toHaveTextContent('Top up EGP 1,250.50 from the account ending 0000?');
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText('Waiting for your bank…')).toBeInTheDocument();
    expect(s.calls.find((c) => c.path === '/v1/topups')?.body).toEqual({ amount_minor: 125050, currency: 'EGP' });
    expect(keys(s, 'POST /v1/topups')[0]).toMatch(/^[0-9a-f-]{36}$/);
    status = 'COMPLETED';
    balance = 125050;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_100);
    });
    expect(await screen.findByText('Top-up complete')).toBeInTheDocument();
    expect(await screen.findByText('New balance: EGP 1,250.50')).toBeInTheDocument();
  });

  it('withdrawal failure shows the bank reason and the refund line (AC-8)', async () => {
    const s = server()
      .on('POST /v1/withdrawals', () => accepted('WITHDRAWAL', 20000))
      .on(`GET /v1/transactions/${TX}`, () =>
        json(200, tx({ id: TX, type: 'WITHDRAWAL', direction: 'OUT', status: 'FAILED', failure_code: 'ACCOUNT_CLOSED' })),
      );
    await renderApp('/withdraw', { server: s, signedIn: true });
    expect(await screen.findByText('Available: EGP 500.00')).toBeInTheDocument();
    const user = await enterAndConfirm('200');
    const dialog = screen.getByRole('dialog', { name: 'Confirm withdrawal' });
    expect(dialog).toHaveTextContent('Withdraw EGP 200.00 to the account ending 0000?');
    expect(dialog).toHaveTextContent('The amount leaves your available balance right away.');
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText(/The bank account is closed\./)).toHaveTextContent('The money is back in your wallet.');
  });

  it('network error → Try again resends with the same key; new amount → new key (AC-10)', async () => {
    let fail = true;
    const s = server().on('POST /v1/topups', () => {
      if (fail) throw new TypeError('network down');
      return accepted('TOPUP', 1000);
    }).on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PENDING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    let user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText("Can't reach the server. Check your connection and try again.")).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText("Can't reach the server. Check your connection and try again.")).toBeInTheDocument();
    const [k1, k2] = keys(s, 'POST /v1/topups');
    expect(k2).toBe(k1);
    // Edit the amount: a new action, a new key.
    user = await enterAndConfirm('11');
    fail = false;
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText('Waiting for your bank…')).toBeInTheDocument();
    const k3 = keys(s, 'POST /v1/topups')[2];
    expect(k3).not.toBe(k1);
  });

  it('503 TRY_AGAIN offers Try again with the same key', async () => {
    let n = 0;
    const s = server()
      .on('POST /v1/topups', () => (n++ === 0 ? apiError(503, 'TRY_AGAIN') : accepted('TOPUP', 1000)))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PENDING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await user.click(await screen.findByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Waiting for your bank…')).toBeInTheDocument();
    const [k1, k2] = keys(s, 'POST /v1/topups');
    expect(k2).toBe(k1);
  });

  it.each([
    ['topups', '/top-up', 'AMOUNT_OUT_OF_RANGE', 'This amount is outside the allowed range for top-ups.'],
    ['withdrawals', '/withdraw', 'AMOUNT_OUT_OF_RANGE', 'This amount is outside the allowed range for withdrawals.'],
    ['withdrawals', '/withdraw', 'INSUFFICIENT_FUNDS', "You don't have enough balance."],
    ['withdrawals', '/withdraw', 'DAILY_LIMIT_EXCEEDED', 'This is above your daily limit for withdrawals and transfers.'],
    ['topups', '/top-up', 'WALLET_LIMIT_EXCEEDED', 'This top-up would take your balance above the maximum allowed.'],
  ])('422 from /v1/%s on %s: %s (AC-11)', async (endpoint, path, code, message) => {
    const s = server().on(`POST /v1/${endpoint}`, () => apiError(422, code));
    await renderApp(path, { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(message);
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });

  it('403 WALLET_FROZEN on submit refetches the wallet and shows the banner (AC-12)', async () => {
    let frozen = false;
    const s = server()
      .on('GET /v1/me/wallet', () => json(200, wallet({ status: frozen ? 'FROZEN' : 'ACTIVE' })))
      .on('POST /v1/topups', () => {
        frozen = true;
        return apiError(403, 'WALLET_FROZEN');
      });
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText(/Your wallet is frozen/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Amount')).not.toBeInTheDocument();
  });

  it('no final state within 60 s: "taking longer" with a link (AC-13)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const s = server()
      .on('POST /v1/topups', () => accepted('TOPUP', 1000))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PROCESSING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Waiting for your bank…');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(61_000);
    });
    expect(await screen.findByText(/This is taking longer than usual/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View transaction' })).toHaveAttribute('href', `/transactions/${TX}`);
    const polls = s.count(`GET /v1/transactions/${TX}`);
    expect(polls).toBeGreaterThanOrEqual(25);
    expect(polls).toBeLessThanOrEqual(32);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(s.count(`GET /v1/transactions/${TX}`)).toBe(polls);
  });

  it('double-clicking Confirm sends one request (AC-14)', async () => {
    const s = server()
      .on('POST /v1/topups', () => new Promise((r) => setTimeout(() => r(accepted('TOPUP', 1000)), 50)))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PENDING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.dblClick(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Waiting for your bank…');
    expect(s.count('POST /v1/topups')).toBe(1);
  });

  it('the confirm dialog keeps Tab inside and Escape goes back (AC-15)', async () => {
    await renderApp('/top-up', { server: server(), signedIn: true });
    const user = await enterAndConfirm('10');
    const dialog = screen.getByRole('dialog');
    const confirm = within(dialog).getByRole('button', { name: 'Confirm' });
    const back = within(dialog).getByRole('button', { name: 'Back' });
    expect(confirm).toHaveFocus();
    await user.tab();
    expect(back).toHaveFocus();
    await user.tab({ shift: true });
    expect(confirm).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Amount')).toHaveValue('10');
  });

  it('a failed wallet refetch after 202 never hides the accepted request (review #1)', async () => {
    let walletCalls = 0;
    const s = server()
      .on('GET /v1/me/wallet', () => (walletCalls++ === 0 ? json(200, wallet()) : apiError(500, 'INTERNAL_ERROR')))
      .on('POST /v1/topups', () => accepted('TOPUP', 1000))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PENDING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByText('Waiting for your bank…')).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 1500)); // let the failing refetch and its retry finish
    expect(walletCalls).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('Waiting for your bank…')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps polling when the first status GET fails, and refreshes the balance when it is already final (review #2, #3)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let txCalls = 0;
    let balance = 0;
    const s = server()
      .on('GET /v1/me/wallet', () => json(200, wallet({ available_balance_minor: balance })))
      .on('POST /v1/topups', () => accepted('TOPUP', 1000))
      .on(`GET /v1/transactions/${TX}`, () => {
        txCalls++;
        if (txCalls <= 2) throw new TypeError('blip'); // first try + its retry
        return json(200, tx({ id: TX, status: 'COMPLETED' }));
      });
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Waiting for your bank…');
    balance = 1000; // the bank credited before our first successful GET
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6_000);
    });
    expect(await screen.findByText('Top-up complete')).toBeInTheDocument();
    expect(await screen.findByText('New balance: EGP 10.00')).toBeInTheDocument();
  });

  it('editing the amount hides an old error and its Try again (review #4)', async () => {
    const s = server().on('POST /v1/topups', () => apiError(503, 'TRY_AGAIN'));
    await renderApp('/top-up', { server: s, signedIn: true });
    const user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('Amount'), '0');
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });

  it('focus moves to the error after a failed confirm and to the status after 202 (review #6)', async () => {
    let fail = true;
    const s = server()
      .on('POST /v1/topups', () => (fail ? apiError(422, 'WALLET_LIMIT_EXCEEDED') : accepted('TOPUP', 1000)))
      .on(`GET /v1/transactions/${TX}`, () => json(200, tx({ id: TX, status: 'PENDING' })));
    await renderApp('/top-up', { server: s, signedIn: true });
    let user = await enterAndConfirm('10');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    const alert = await screen.findByRole('alert');
    await vi.waitFor(() => expect(alert.parentElement).toHaveFocus());
    fail = false;
    user = await enterAndConfirm('11');
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Waiting for your bank…');
    await vi.waitFor(() => expect(screen.getByTestId('tx-status')).toHaveFocus());
  });

  it('the rest of the page is inert while the dialog is open (review #7)', async () => {
    const { container } = await renderApp('/top-up', { server: server(), signedIn: true });
    const user = await enterAndConfirm('10');
    expect(container.inert).toBe(true);
    await user.keyboard('{Escape}');
    expect(container.inert).toBe(false);
  });
});
