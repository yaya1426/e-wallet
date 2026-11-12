import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { FakeServer, json, tokens } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';
import { safeNext } from './next';

const wallet = { wallet_id: 'w-1', currency: 'EGP', available_balance_minor: 0, status: 'ACTIVE' };

describe('route guards (AC-16)', () => {
  it('sends an anonymous visitor to /login?next=… and back after login', async () => {
    const server = new FakeServer()
      .on('POST /v1/auth/login', () => json(200, tokens(1)))
      .on('GET /v1/me/wallet', () => json(200, wallet));
    const { router } = await renderApp('/', { server });
    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/login');
    expect(router.state.location.search).toBe('?next=%2F');

    const user = userEvent.setup();
    await user.type(screen.getByLabelText('Mobile number'), '01001234567');
    await user.type(screen.getByLabelText('Password'), 'secret123');
    await user.click(screen.getByRole('button', { name: 'Log in' }));
    expect(await screen.findByTestId('balance')).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
  });

  it.each(['%2F%2Fevil.example', '%2F%09%2Fevil.example', '%2F%5Cevil.example'])('ignores an off-site next %s', async (next) => {
    const server = new FakeServer()
      .on('POST /v1/auth/login', () => json(200, tokens(1)))
      .on('GET /v1/me/wallet', () => json(200, wallet));
    const { router } = await renderApp(`/login?next=${next}`, { server });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('Mobile number'), '01001234567');
    await user.type(screen.getByLabelText('Password'), 'secret123');
    await user.click(screen.getByRole('button', { name: 'Log in' }));
    expect(await screen.findByTestId('balance')).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
  });

  it('sends a signed-in user away from the auth pages', async () => {
    const server = new FakeServer().on('GET /v1/me/wallet', () => json(200, wallet));
    const { router } = await renderApp('/register', { server, signedIn: true });
    expect(await screen.findByTestId('balance')).toBeInTheDocument();
    expect(router.state.location.pathname).toBe('/');
  });

  it('unknown paths show not found', async () => {
    await renderApp('/nope');
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
  });

  it.each([
    [null, '/'],
    ['', '/'],
    ['/', '/'],
    ['/history?page=2', '/history?page=2'],
    ['//evil.example', '/'],
    ['/\\evil.example', '/'],
    ['https://evil.example', '/'],
    ['javascript:alert(1)', '/'],
    ['/\t/evil.example', '/'],
    ['/\n\\evil.example', '/'],
    ['/%09/evil.example', '/%09/evil.example'],
    ['/a/../b?x=1#h', '/b?x=1#h'],
  ])('safeNext(%j) = %j', (input, want) => {
    expect(safeNext(input)).toBe(want);
  });
});
