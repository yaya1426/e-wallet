import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';

describe('HomePlaceholder', () => {
  it('shows the formatted balance and a frozen notice', async () => {
    const server = new FakeServer().on('GET /v1/me/wallet', () =>
      json(200, { wallet_id: 'w', currency: 'EGP', available_balance_minor: 123456789, status: 'FROZEN' }),
    );
    await renderApp('/', { server, signedIn: true });
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 1,234,567.89');
    expect(screen.getByText(/Your wallet is frozen/)).toBeInTheDocument();
  });

  it('shows an error with a retry', async () => {
    let ok = false;
    const server = new FakeServer().on('GET /v1/me/wallet', () =>
      ok
        ? json(200, { wallet_id: 'w', currency: 'EGP', available_balance_minor: 5, status: 'ACTIVE' })
        : apiError(500, 'INTERNAL_ERROR', 'req-1'),
    );
    await renderApp('/', { server, signedIn: true });
    expect(await screen.findByRole('alert', {}, { timeout: 3000 })).toHaveTextContent('Reference: req-1');
    ok = true;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 0.05');
  });

  it('sign out calls the API and returns to login (AC-14)', async () => {
    const server = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, { wallet_id: 'w', currency: 'EGP', available_balance_minor: 0, status: 'ACTIVE' }))
      .on('POST /v1/auth/logout', () => new Response(null, { status: 204 }));
    const { env, router } = await renderApp('/', { server, signedIn: true });
    await screen.findByTestId('balance');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Sign out' }));
    expect(await screen.findByText('You have signed out.')).toBeInTheDocument();
    expect(router.state.location.pathname + router.state.location.search).toBe('/login');
    expect(server.count('POST /v1/auth/logout')).toBe(1);
    expect(env.storage.map.size).toBe(0);
  });
});
