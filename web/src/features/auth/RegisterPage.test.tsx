import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json, tokens } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';

const wallet = { wallet_id: 'w-1', currency: 'EGP', available_balance_minor: 0, status: 'ACTIVE' };

async function fill(values: { phone?: string; name?: string; password?: string; confirm?: string }) {
  const user = userEvent.setup();
  if (values.phone) await user.type(screen.getByLabelText('Mobile number'), values.phone);
  if (values.name) await user.type(screen.getByLabelText('Full name'), values.name);
  if (values.password) await user.type(screen.getByLabelText('Password'), values.password);
  if (values.confirm) await user.type(screen.getByLabelText('Confirm password'), values.confirm);
  await user.click(screen.getByRole('button', { name: 'Sign up' }));
}

const valid = { phone: '01001234567', name: '  Ahmed Ali ', password: 'secret123', confirm: 'secret123' };

describe('RegisterPage', () => {
  it('registers, logs in automatically and lands on home (AC-5)', async () => {
    const server = new FakeServer()
      .on('POST /v1/auth/register', () => json(201, {}))
      .on('POST /v1/auth/login', () => json(200, tokens(1)))
      .on('GET /v1/me/wallet', () => json(200, wallet));
    await renderApp('/register', { server });
    await fill(valid);
    expect(await screen.findByTestId('balance')).toHaveTextContent('EGP 0.00');
    expect(server.calls[0]?.body).toEqual({ phone: '+201001234567', full_name: 'Ahmed Ali', password: 'secret123' });
    expect(server.calls[1]?.body).toEqual({ phone: '+201001234567', password: 'secret123' });
  });

  it('shows all client errors and sends nothing (AC-7)', async () => {
    const server = new FakeServer();
    await renderApp('/register', { server });
    await fill({ phone: '0123', name: 'A', password: 'short', confirm: 'other' });
    expect(screen.getByText(/Enter a valid Egyptian mobile number/)).toBeInTheDocument();
    expect(screen.getByText('Name must be 2 to 100 characters.')).toBeInTheDocument();
    expect(screen.getByText('Password must be 8 to 72 characters.')).toBeInTheDocument();
    expect(screen.getByText('The passwords do not match.')).toBeInTheDocument();
    expect(screen.getByLabelText('Full name')).toHaveAccessibleDescription('Name must be 2 to 100 characters.');
    expect(server.calls).toHaveLength(0);
  });

  it('mismatched confirmation alone blocks the submit (AC-7)', async () => {
    const server = new FakeServer();
    await renderApp('/register', { server });
    await fill({ ...valid, confirm: 'secret124' });
    expect(screen.getByText('The passwords do not match.')).toBeInTheDocument();
    expect(server.calls).toHaveLength(0);
  });

  it('a registered phone shows the message on the phone field with a login link (AC-6)', async () => {
    const server = new FakeServer().on('POST /v1/auth/register', () => apiError(409, 'PHONE_ALREADY_REGISTERED'));
    await renderApp('/register', { server });
    await fill(valid);
    const phone = screen.getByLabelText('Mobile number');
    expect(await screen.findByText('This phone number is already registered. Log in instead.')).toBeInTheDocument();
    expect(phone).toHaveAttribute('aria-invalid', 'true');
    await userEvent.setup().click(screen.getByRole('link', { name: 'Log in instead' }));
    expect(await screen.findByRole('heading', { name: 'Log in' })).toBeInTheDocument();
    expect(screen.getByLabelText('Mobile number')).toHaveValue('+201001234567');
  });

  it('maps server field errors to their fields', async () => {
    const server = new FakeServer().on('POST /v1/auth/register', () => apiError(400, 'INVALID_NAME'));
    await renderApp('/register', { server });
    await fill(valid);
    expect(await screen.findByText('Name must be 2 to 100 characters.')).toBeInTheDocument();
    expect(screen.getByLabelText('Full name')).toHaveAttribute('aria-invalid', 'true');
  });

  it('shows the rate-limit message as a form error', async () => {
    const server = new FakeServer().on('POST /v1/auth/register', () => apiError(429, 'TOO_MANY_REQUESTS'));
    await renderApp('/register', { server });
    await fill(valid);
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many attempts.');
  });

  it('when the automatic login fails, opens login with the phone and a notice', async () => {
    const server = new FakeServer()
      .on('POST /v1/auth/register', () => json(201, {}))
      .on('POST /v1/auth/login', () => apiError(429, 'TOO_MANY_REQUESTS'));
    await renderApp('/register', { server });
    await fill(valid);
    expect(await screen.findByText('Account created, please log in.')).toBeInTheDocument();
    expect(screen.getByLabelText('Mobile number')).toHaveValue('+201001234567');
  });
});
