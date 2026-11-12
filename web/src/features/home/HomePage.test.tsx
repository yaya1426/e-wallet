import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { page, tx, wallet, WALLET_ID } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

function server(w = wallet(), items = [tx()]) {
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, w))
    .on('GET /v1/me/transactions', () => json(200, page(items)));
}

afterEach(() => {
  vi.useRealTimers();
});

describe('HomePage (T031)', () => {
  it('shows the balance, recent transactions and a copyable wallet ID (AC-1)', async () => {
    const user = userEvent.setup(); // installs a clipboard stub we can spy on
    const writeText = vi.spyOn(navigator.clipboard, 'writeText');
    const s = server(wallet({ available_balance_minor: 15075 }));
    await renderApp('/', { server: s, signedIn: true });
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 150.75');
    expect(screen.getByTestId('wallet-id')).toHaveTextContent(WALLET_ID);
    expect(await screen.findByText('Top-up')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /Copy Wallet ID/ }));
    expect(writeText).toHaveBeenCalledWith(WALLET_ID);
    expect(await screen.findByRole('button', { name: /Copy Wallet ID/ })).toHaveTextContent('Copied');
    expect(screen.getByText('Copied to the clipboard.')).toBeInTheDocument();
    // Recent list asks for 5 items only.
    expect(s.fetch.mock.calls.some(([r]) => new URL((r as Request).url).search === '?limit=5')).toBe(true);
  });

  it('a failed copy tells the user to copy by hand', async () => {
    const user = userEvent.setup();
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('denied'));
    await renderApp('/', { server: server(), signedIn: true });
    await user.click(await screen.findByRole('button', { name: /Copy Wallet ID/ }));
    expect(await screen.findByText(/Couldn't copy/)).toBeInTheDocument();
  });

  it('frozen wallet: banner and disabled, focusable actions with the reason (AC-2)', async () => {
    const { router } = await renderApp('/', { server: server(wallet({ status: 'FROZEN' })), signedIn: true });
    expect(await screen.findByText(/Your wallet is frozen/)).toBeInTheDocument();
    const main = screen.getByRole('main');
    for (const name of ['Top up', 'Withdraw', 'Send']) {
      const action = within(main).getByRole('button', { name });
      expect(action).toHaveAttribute('aria-disabled', 'true');
      expect(action).toHaveAccessibleDescription('Not available while your wallet is frozen.');
    }
    // Send is disabled in the navigation too (UI-BR-14 "everywhere").
    const nav = screen.getByRole('navigation', { name: 'Main' });
    expect(within(nav).getByRole('button', { name: 'Send' })).toHaveAttribute('aria-disabled', 'true');
    const topUp = within(main).getByRole('button', { name: 'Top up' });
    topUp.focus();
    expect(topUp).toHaveFocus();
    await userEvent.setup().click(topUp);
    expect(router.state.location.pathname).toBe('/');
  });

  it('active wallet: actions link to their screens', async () => {
    await renderApp('/', { server: server(), signedIn: true });
    await screen.findByTestId('balance');
    expect(screen.getByRole('link', { name: 'Top up' })).toHaveAttribute('href', '/top-up');
    expect(screen.getByRole('link', { name: 'Withdraw' })).toHaveAttribute('href', '/withdraw');
  });

  it('empty history shows "No transactions yet." (AC-3)', async () => {
    await renderApp('/', { server: server(wallet(), []), signedIn: true });
    expect(await screen.findByText('No transactions yet.')).toBeInTheDocument();
  });

  it('balance error shows the mapped message with a retry', async () => {
    let ok = false;
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => (ok ? json(200, wallet({ available_balance_minor: 5 })) : apiError(500, 'INTERNAL_ERROR', 'req-1')))
      .on('GET /v1/me/transactions', () => json(200, page([])));
    await renderApp('/', { server: s, signedIn: true });
    expect(await screen.findByRole('alert', {}, { timeout: 3000 })).toHaveTextContent('Reference: req-1');
    ok = true;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 0.05');
  });

  it('polls while a transaction is pending and stops when it completes (AC-8)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let status: 'PENDING' | 'COMPLETED' = 'PENDING';
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, wallet()))
      .on('GET /v1/me/transactions', () => json(200, page([tx({ id: 'aaaaaaaa-0000-7000-8000-000000000001', status })])));
    await renderApp('/', { server: s, signedIn: true });
    expect(await screen.findByText('Pending')).toBeInTheDocument();
    const before = s.count('GET /v1/me/transactions');
    status = 'COMPLETED';
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_100);
    });
    await waitFor(() => expect(screen.getByText('Completed')).toBeInTheDocument());
    const after = s.count('GET /v1/me/transactions');
    expect(after).toBe(before + 1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000);
    });
    expect(s.count('GET /v1/me/transactions')).toBe(after);
  });

  it('stops polling a pending transaction after 2 minutes (AC-8)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, wallet()))
      .on('GET /v1/me/transactions', () => json(200, page([tx({ status: 'PROCESSING' })])));
    await renderApp('/', { server: s, signedIn: true });
    await screen.findByText('Pending');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(125_000);
    });
    const n = s.count('GET /v1/me/transactions');
    expect(n).toBeGreaterThanOrEqual(20);
    expect(n).toBeLessThanOrEqual(26);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(s.count('GET /v1/me/transactions')).toBe(n);
  });
  it('refetches the balance when a pending transaction completes (UI-BR-19)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let done = false;
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, wallet({ available_balance_minor: done ? 50000 : 0 })))
      .on('GET /v1/me/transactions', () =>
        json(200, page([tx({ id: 'aaaaaaaa-0000-7000-8000-000000000002', status: done ? 'COMPLETED' : 'PENDING' })])),
      );
    await renderApp('/', { server: s, signedIn: true });
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 0.00');
    await screen.findByText('Pending');
    done = true;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_100);
    });
    await waitFor(() => expect(screen.getByTestId('balance')).toHaveTextContent('EGP 500.00'));
  });

  it('a new pending item after the 2-minute window gets its own polling window', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let status: 'PENDING' | 'COMPLETED' = 'COMPLETED';
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, wallet()))
      .on('GET /v1/me/transactions', () => json(200, page([tx({ status })])));
    const { queryClient } = await renderApp('/', { server: s, signedIn: true });
    await screen.findByText('Completed');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200_000);
    });
    status = 'PENDING';
    await act(async () => {
      await queryClient.invalidateQueries();
    });
    await screen.findByText('Pending');
    const n = s.count('GET /v1/me/transactions');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_200);
    });
    expect(s.count('GET /v1/me/transactions')).toBe(n + 2);
  });
});
