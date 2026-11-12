import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';

async function fill(values: { phone: string; code: string; password: string; confirm: string }) {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText('Mobile number'), values.phone);
  await user.type(screen.getByLabelText('Reset code'), values.code);
  await user.type(screen.getByLabelText('New password'), values.password);
  await user.type(screen.getByLabelText('Confirm password'), values.confirm);
  await user.click(screen.getByRole('button', { name: 'Set new password' }));
}

const valid = { phone: '01001234567', code: '123456', password: 'newsecret1', confirm: 'newsecret1' };

describe('ResetPasswordPage (AC-15)', () => {
  it('explains where the code comes from', async () => {
    await renderApp('/reset-password');
    expect(screen.getByText(/Contact support to get a 6-digit reset code/)).toBeInTheDocument();
  });

  it('resets and opens login with the success notice and phone', async () => {
    const server = new FakeServer().on('POST /v1/auth/password-reset', () => new Response(null, { status: 204 }));
    await renderApp('/reset-password', { server });
    await fill(valid);
    expect(await screen.findByText('Password changed. Please log in.')).toBeInTheDocument();
    expect(screen.getByLabelText('Mobile number')).toHaveValue('+201001234567');
    expect(server.calls[0]?.body).toEqual({ phone: '+201001234567', code: '123456', new_password: 'newsecret1' });
  });

  it('a pasted code with spaces or dashes keeps only its 6 digits', async () => {
    await renderApp('/reset-password');
    const user = userEvent.setup();
    await user.click(screen.getByLabelText('Reset code'));
    await user.paste(' 123-456 ');
    expect(screen.getByLabelText('Reset code')).toHaveValue('123456');
  });

  it('wrong code shows the reset-code message', async () => {
    const server = new FakeServer().on('POST /v1/auth/password-reset', () => apiError(401, 'INVALID_RESET_CODE'));
    await renderApp('/reset-password', { server });
    await fill(valid);
    expect(await screen.findByRole('alert')).toHaveTextContent('The code is wrong or expired. Ask support for a new one.');
  });

  it('checks the code and passwords before calling the API', async () => {
    const server = new FakeServer();
    await renderApp('/reset-password', { server });
    await fill({ ...valid, code: '12a45', confirm: 'different1' });
    expect(screen.getByText('Enter the 6-digit code.')).toBeInTheDocument();
    expect(screen.getByText('The passwords do not match.')).toBeInTheDocument();
    expect(server.calls).toHaveLength(0);
  });
});
