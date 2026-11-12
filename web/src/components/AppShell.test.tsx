import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { FakeServer, json } from '@/test/fakes';
import { page, tx, wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

function server() {
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, wallet()))
    .on('GET /v1/me/transactions', () => json(200, page([tx({ status: 'FAILED', amount_minor: 1234 })])));
}

describe('AppShell (AC-11)', () => {
  it('marks the active link and navigates with the keyboard', async () => {
    const { router } = await renderApp('/', { server: server(), signedIn: true });
    const nav = await screen.findByRole('navigation', { name: 'Main' });
    expect(within(nav).getByRole('link', { name: 'Home' })).toHaveAttribute('aria-current', 'page');
    const history = within(nav).getByRole('link', { name: 'History' });
    history.focus();
    await userEvent.setup().keyboard('{Enter}');
    expect(router.state.location.pathname).toBe('/history');
    expect(within(nav).getByRole('link', { name: 'History' })).toHaveAttribute('aria-current', 'page');
  });

  it('later screens show a placeholder until their task lands', async () => {
    await renderApp('/bank-account', { server: server(), signedIn: true });
    expect(await screen.findByRole('heading', { name: 'Bank account' })).toBeInTheDocument();
    expect(screen.getByText('This screen is not available yet.')).toBeInTheDocument();
  });

  it('a failed row strikes through the amount and says no money moved (AC-4)', async () => {
    await renderApp('/', { server: server(), signedIn: true });
    const amount = await screen.findByTestId('tx-amount');
    expect(amount.querySelector('s')).toHaveTextContent('+EGP 12.34');
    expect(amount).toHaveTextContent('(no money moved)');
    expect(screen.getByText('12 Nov 2026, 14:05')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('sign out returns to login', async () => {
    const s = server().on('POST /v1/auth/logout', () => new Response(null, { status: 204 }));
    const { router } = await renderApp('/', { server: s, signedIn: true });
    await screen.findByTestId('balance');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Sign out' }));
    expect(await screen.findByText('You have signed out.')).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/login');
    expect(s.count('POST /v1/auth/logout')).toBe(1);
  });
});
