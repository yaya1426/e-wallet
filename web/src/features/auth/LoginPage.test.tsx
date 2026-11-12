import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { STORAGE_KEY } from '@/auth/session';
import { apiError, FakeServer, json, tokens } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';

const wallet = { wallet_id: 'w-1', currency: 'EGP', available_balance_minor: 0, status: 'ACTIVE' };

async function fillAndSubmit(phone: string, password: string) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText('Mobile number'), phone);
  await user.type(screen.getByLabelText('Password'), password);
  await user.click(screen.getByRole('button', { name: 'Log in' }));
  return user;
}

describe('LoginPage', () => {
  it('logs in with a local-format phone and lands on home (AC-8)', async () => {
    const server = new FakeServer()
      .on('POST /v1/auth/login', () => json(200, tokens(1)))
      .on('GET /v1/me/wallet', () => json(200, wallet));
    await renderApp('/login', { server });
    await fillAndSubmit('0100 123 4567', 'secret123');
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 0.00');
    expect(server.calls[0]?.body).toEqual({ phone: '+201001234567', password: 'secret123' });
  });

  it('shows the wrong-credentials message, never the server text (AC-8, UI-BR-4)', async () => {
    const server = new FakeServer().on('POST /v1/auth/login', () => apiError(401, 'INVALID_CREDENTIALS'));
    await renderApp('/login', { server });
    await fillAndSubmit('01001234567', 'wrongpass');
    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong phone number or password.');
    expect(screen.queryByText(/server text/)).not.toBeInTheDocument();
  });

  it('rejects an invalid phone without calling the API', async () => {
    const server = new FakeServer();
    await renderApp('/login', { server });
    await fillAndSubmit('12345', 'secret123');
    expect(await screen.findByText(/Enter a valid Egyptian mobile number/)).toBeInTheDocument();
    expect(screen.getByLabelText('Mobile number')).toHaveAttribute('aria-invalid', 'true');
    await waitFor(() => expect(screen.getByLabelText('Mobile number')).toHaveFocus());
    expect(server.calls).toHaveLength(0);
  });

  it.each([
    [apiError(429, 'TOO_MANY_REQUESTS'), 'Too many attempts. Please wait and try again.'],
    [apiError(403, 'USER_DISABLED'), 'Your account is disabled. Please contact support.'],
    [apiError(500, 'INTERNAL_ERROR', 'req-99'), 'Something went wrong. Please try again. (Reference: req-99)'],
    [apiError(418, 'SOMETHING_NEW', 'req-5'), 'Something went wrong. Please try again. (Reference: req-5)'],
  ])('maps API errors to messages (AC-17) %#', async (response, message) => {
    const server = new FakeServer().on('POST /v1/auth/login', () => response.clone());
    await renderApp('/login', { server });
    await fillAndSubmit('01001234567', 'secret123');
    expect(await screen.findByRole('alert')).toHaveTextContent(message);
  });

  it('shows the connection message on a network failure (AC-17)', async () => {
    const server = new FakeServer().on('POST /v1/auth/login', () => {
      throw new TypeError('Failed to fetch');
    });
    await renderApp('/login', { server });
    await fillAndSubmit('01001234567', 'secret123');
    expect(await screen.findByRole('alert')).toHaveTextContent("Can't reach the server.");
  });

  it('shows "session ended" after a rejected refresh (AC-12)', async () => {
    const server = new FakeServer().on('POST /v1/auth/refresh', () => apiError(401, 'INVALID_REFRESH_TOKEN'));
    await renderApp('/', {
      server,
      before: (env) =>
        env.storage.setItem(STORAGE_KEY, JSON.stringify({ refresh_token: 'r', refresh_expires_at: env.clock.now + 1e6 })),
    });
    expect(await screen.findByText('Your session has ended. Please log in again.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Log in' })).toBeInTheDocument();
  });

  it('shows the disabled message when the user is disabled (AC-13)', async () => {
    const server = new FakeServer().on('POST /v1/auth/refresh', () => apiError(403, 'USER_DISABLED'));
    await renderApp('/', {
      server,
      before: (env) =>
        env.storage.setItem(STORAGE_KEY, JSON.stringify({ refresh_token: 'r', refresh_expires_at: env.clock.now + 1e6 })),
    });
    expect(await screen.findByText('Your account is disabled. Please contact support.')).toBeInTheDocument();
  });

  it('offers a retry when the server could not be reached at startup', async () => {
    let up = false;
    const server = new FakeServer()
      .on('POST /v1/auth/refresh', () => {
        if (!up) throw new TypeError('offline');
        return json(200, tokens(1));
      })
      .on('GET /v1/me/wallet', () => json(200, wallet));
    await renderApp('/', {
      server,
      before: (env) =>
        env.storage.setItem(STORAGE_KEY, JSON.stringify({ refresh_token: 'r', refresh_expires_at: env.clock.now + 1e6 })),
    });
    expect(await screen.findByText(/Can't reach the server/)).toBeInTheDocument();
    up = true;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByTestId('balance')).toBeInTheDocument();
  });

  it('can be used with the keyboard only (AC-19)', async () => {
    const server = new FakeServer()
      .on('POST /v1/auth/login', () => json(200, tokens(1)))
      .on('GET /v1/me/wallet', () => json(200, wallet));
    await renderApp('/login', { server });
    const user = userEvent.setup();
    await user.tab();
    expect(screen.getByLabelText('Mobile number')).toHaveFocus();
    await user.keyboard('01001234567');
    await user.tab();
    expect(screen.getByLabelText('Password')).toHaveFocus();
    await user.keyboard('secret123{Enter}');
    expect(await screen.findByTestId('balance')).toBeInTheDocument();
  });
});
