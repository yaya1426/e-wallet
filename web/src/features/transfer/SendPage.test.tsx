import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

const TX = '0193a0b2-0000-7000-8000-0000000000f2';
const B = { phone: '+201112223333', masked_name: 'Mona S. K.' };

function transfer(amount: number, balanceAfter: number, note: string | null = null) {
  return json(201, {
    transaction_id: TX,
    type: 'P2P',
    status: 'COMPLETED',
    amount_minor: amount,
    currency: 'EGP',
    recipient: B,
    note,
    balance_after_minor: balanceAfter,
    created_at: '2026-11-12T12:05:00Z',
  });
}

function server(opts: { status?: 'ACTIVE' | 'FROZEN'; balance?: number } = {}) {
  return new FakeServer()
    .on('GET /v1/me/wallet', () => json(200, wallet({ status: opts.status ?? 'ACTIVE', available_balance_minor: opts.balance ?? 50000 })))
    .on('GET /v1/me/transactions', () => json(200, { items: [], next_cursor: null }))
    .on('GET /v1/recipients', () => json(200, B));
}

const requests = (s: FakeServer, route: string) =>
  s.fetch.mock.calls.map(([r]) => r as Request).filter((r) => `${r.method} ${new URL(r.url).pathname}` === route);
const keys = (s: FakeServer) => requests(s, 'POST /v1/transfers').map((r) => r.headers.get('Idempotency-Key'));

async function toAmountStep(phone = '0111 222 3333') {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText('Recipient mobile number'), phone);
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  expect(await screen.findByText('Send to Mona S. K.')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await screen.findByLabelText('Amount');
  return user;
}

async function toConfirmStep(amount = '50', note = '') {
  const user = await toAmountStep();
  await user.type(screen.getByLabelText('Amount'), amount);
  if (note) await user.type(screen.getByLabelText('Note (optional)'), note);
  await user.click(screen.getByRole('button', { name: 'Continue' }));
  await screen.findByRole('heading', { name: 'Check and send' });
  return user;
}

describe('Send money (T033)', () => {
  it('happy path: preview, amount and note, confirm, result (AC-1, AC-9)', async () => {
    const s = server().on('POST /v1/transfers', () => transfer(5000, 45000, 'lunch'));
    await renderApp('/send', { server: s, signedIn: true });
    expect(await screen.findByText('Step 1 of 3')).toBeInTheDocument();
    const user = await toAmountStep();
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument();
    expect(screen.getByLabelText('Amount')).toHaveFocus();
    expect(screen.getByText('Available: EGP 500.00')).toBeInTheDocument();
    await user.type(screen.getByLabelText('Amount'), '50');
    await user.type(screen.getByLabelText('Note (optional)'), '  lunch ');
    expect(screen.getByText('5/140')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByText('Step 3 of 3')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Check and send' })).toHaveFocus();
    expect(screen.getByText('Mona S. K.')).toBeInTheDocument();
    expect(screen.getByText('(+20 111 222 3333)')).toBeInTheDocument();
    expect(screen.getByText('EGP 50.00')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(await screen.findByText('Sent EGP 50.00 to Mona S. K.')).toBeInTheDocument();
    expect(screen.getByText('New balance: EGP 450.00')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View transaction' })).toHaveAttribute('href', `/transactions/${TX}`);
    expect(s.calls.find((c) => c.path === '/v1/transfers')?.body).toEqual({
      recipient_phone: '+201112223333',
      amount_minor: 5000,
      currency: 'EGP',
      note: 'lunch',
    });
    expect(keys(s)[0]).toMatch(/^[0-9a-f-]{36}$/);
    // Send again starts a fresh flow.
    await user.click(screen.getByRole('button', { name: 'Send again' }));
    expect(await screen.findByLabelText('Recipient mobile number')).toHaveValue('');
  });

  it('an empty note is left out of the request', async () => {
    const s = server().on('POST /v1/transfers', () => transfer(5000, 45000));
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50', '   ');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    await screen.findByText('Sent EGP 50.00 to Mona S. K.');
    expect(s.calls.find((c) => c.path === '/v1/transfers')?.body).toEqual({ recipient_phone: '+201112223333', amount_minor: 5000, currency: 'EGP' });
  });

  it('the preview request encodes "+" as %2B (AC-4)', async () => {
    const s = server();
    await renderApp('/send', { server: s, signedIn: true });
    await toAmountStep();
    const url = requests(s, 'GET /v1/recipients')[0]?.url ?? '';
    expect(url).toContain('phone=%2B201112223333');
  });

  it('bad phone format: field error and no request (AC-2)', async () => {
    const s = server();
    await renderApp('/send', { server: s, signedIn: true });
    const user = userEvent.setup();
    const field = await screen.findByLabelText('Recipient mobile number');
    await user.type(field, '12345');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(field).toHaveAccessibleDescription('Enter a valid Egyptian mobile number, like 01001234567.');
    expect(field).toHaveFocus();
    expect(s.count('GET /v1/recipients')).toBe(0);
  });

  it.each([
    [404, 'RECIPIENT_NOT_FOUND', 'No wallet uses this number.'],
    [422, 'SELF_TRANSFER_NOT_ALLOWED', "You can't send money to yourself."],
    [400, 'INVALID_PHONE', 'Enter a valid Egyptian mobile number, like 01001234567.'],
  ])('preview %i %s shows "%s" under the field (AC-2)', async (status, code, message) => {
    const s = server().on('GET /v1/recipients', () => apiError(status, code));
    await renderApp('/send', { server: s, signedIn: true });
    const user = userEvent.setup();
    const field = await screen.findByLabelText('Recipient mobile number');
    await user.type(field, '01112223333');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await screen.findByText(message);
    expect(field).toHaveAccessibleDescription(message);
    expect(screen.queryByLabelText('Amount')).not.toBeInTheDocument();
  });

  it('Change goes back to the phone field with the number kept', async () => {
    await renderApp('/send?to=01112223333', { server: server(), signedIn: true });
    const user = userEvent.setup();
    expect(await screen.findByLabelText('Recipient mobile number')).toHaveValue('01112223333');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.click(await screen.findByRole('button', { name: 'Change' }));
    const field = screen.getByLabelText('Recipient mobile number');
    expect(field).toHaveValue('01112223333');
    expect(field).toHaveFocus();
  });

  it('amount above the available balance and a 141-character note are rejected in the form (AC-3)', async () => {
    const s = server({ balance: 1000 });
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toAmountStep();
    await user.type(screen.getByLabelText('Amount'), '10.01');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByLabelText('Amount')).toHaveAccessibleDescription("You don't have enough balance.");
    await user.clear(screen.getByLabelText('Amount'));
    await user.type(screen.getByLabelText('Amount'), '10');
    const note = screen.getByLabelText('Note (optional)');
    await user.click(note);
    await user.paste('x'.repeat(141));
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(note).toHaveAccessibleDescription('141/140 Note must be at most 140 characters.');
    expect(note).toHaveFocus();
    await user.type(note, '{Backspace}');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(await screen.findByRole('heading', { name: 'Check and send' })).toBeInTheDocument();
    expect(s.count('POST /v1/transfers')).toBe(0);
  });

  it('network error / 503: Try again resends with the same key; Back + new amount uses a new key (AC-5)', async () => {
    let n = 0;
    const s = server().on('POST /v1/transfers', () => {
      n++;
      if (n === 1) throw new TypeError('network down');
      if (n === 2) return apiError(503, 'TRY_AGAIN');
      return transfer(6000, 44000);
    });
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(await screen.findByText("Can't reach the server. Check your connection and try again.")).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Try again' })).toHaveFocus();
    await user.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('The server was busy. Please try again.')).toBeInTheDocument();
    const [k1, k2] = keys(s);
    expect(k2).toBe(k1);
    await user.click(screen.getByRole('button', { name: 'Back' }));
    await user.clear(screen.getByLabelText('Amount'));
    await user.type(screen.getByLabelText('Amount'), '60');
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.click(await screen.findByRole('button', { name: 'Send' }));
    await screen.findByText('Sent EGP 60.00 to Mona S. K.');
    expect(keys(s)[2]).not.toBe(k1);
  });

  it.each([
    ['INSUFFICIENT_FUNDS', "You don't have enough balance."],
    ['DAILY_LIMIT_EXCEEDED', 'This is above your daily limit for withdrawals and transfers.'],
    ['RECIPIENT_LIMIT_EXCEEDED', "The recipient can't receive this amount right now."],
    ['AMOUNT_OUT_OF_RANGE', 'This amount is outside the allowed range for transfers.'],
  ])('422 %s goes back to step 2 with the message (AC-6)', async (code, message) => {
    let n = 0;
    const s = server().on('POST /v1/transfers', () => (n++ === 0 ? apiError(422, code) : transfer(5000, 45000)));
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    const amount = await screen.findByLabelText('Amount');
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument();
    expect(amount).toHaveAccessibleDescription(message);
    expect(amount).toHaveFocus();
    // The same values sent again are a new action: the key was dropped with the final answer.
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    await user.click(await screen.findByRole('button', { name: 'Send' }));
    await screen.findByText('Sent EGP 50.00 to Mona S. K.');
    const [k1, k2] = keys(s);
    expect(k2).not.toBe(k1);
  });

  it('INSUFFICIENT_FUNDS on send refetches the balance shown on step 2 (AC-6 race)', async () => {
    let balance = 50000;
    const s = server()
      .on('GET /v1/me/wallet', () => json(200, wallet({ available_balance_minor: balance })))
      .on('POST /v1/transfers', () => {
        balance = 1000;
        return apiError(422, 'INSUFFICIENT_FUNDS');
      });
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(await screen.findByText('Available: EGP 10.00')).toBeInTheDocument();
    // The form check now uses the new balance too.
    await user.click(screen.getByRole('button', { name: 'Continue' }));
    expect(screen.getByLabelText('Amount')).toHaveAccessibleDescription("You don't have enough balance.");
    expect(s.count('POST /v1/transfers')).toBe(1);
  });

  it('404 RECIPIENT_NOT_FOUND on send goes back to step 1 with the message (AC-6)', async () => {
    const s = server().on('POST /v1/transfers', () => apiError(404, 'RECIPIENT_NOT_FOUND'));
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    const field = await screen.findByLabelText('Recipient mobile number');
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument();
    expect(field).toHaveAccessibleDescription('No wallet uses this number.');
    expect(field).toHaveValue('0111 222 3333');
  });

  it('frozen wallet shows the banner instead of the flow (AC-7)', async () => {
    await renderApp('/send', { server: server({ status: 'FROZEN' }), signedIn: true });
    expect(await screen.findByText(/Your wallet is frozen/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Recipient mobile number')).not.toBeInTheDocument();
  });

  it('a stale submit answered 403 WALLET_FROZEN shows the frozen message (AC-7)', async () => {
    let frozen = false;
    const s = server()
      .on('GET /v1/me/wallet', () => json(200, wallet({ status: frozen ? 'FROZEN' : 'ACTIVE' })))
      .on('POST /v1/transfers', () => {
        frozen = true;
        return apiError(403, 'WALLET_FROZEN');
      });
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    await user.click(screen.getByRole('button', { name: 'Send' }));
    expect(await screen.findByText(/Your wallet is frozen/)).toBeInTheDocument();
    // The wallet is refetched and the frozen banner replaces the flow.
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Check and send' })).not.toBeInTheDocument());
    expect(screen.getByText(/Your wallet is frozen/)).toBeInTheDocument();
    expect(s.count('GET /v1/me/wallet')).toBeGreaterThanOrEqual(2);
  });

  it('Send is disabled while in flight and a double click sends one request (AC-8)', async () => {
    const s = server().on('POST /v1/transfers', () => new Promise((r) => setTimeout(() => r(transfer(5000, 45000)), 50)));
    await renderApp('/send', { server: s, signedIn: true });
    const user = await toConfirmStep('50');
    const send = screen.getByRole('button', { name: 'Send' });
    await user.dblClick(send);
    expect(send).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();
    await screen.findByText('Sent EGP 50.00 to Mona S. K.');
    expect(s.count('POST /v1/transfers')).toBe(1);
  });

  it('two Send clicks in the same tick still send one request (in-flight guard)', async () => {
    const s = server().on('POST /v1/transfers', () => new Promise((r) => setTimeout(() => r(transfer(5000, 45000)), 50)));
    await renderApp('/send', { server: s, signedIn: true });
    await toConfirmStep('50');
    const send = screen.getByRole('button', { name: 'Send' });
    act(() => {
      send.click();
      send.click(); // React has not re-rendered yet: the button is still enabled
    });
    await screen.findByText('Sent EGP 50.00 to Mona S. K.');
    expect(s.count('POST /v1/transfers')).toBe(1);
  });

  it('every step is keyboard usable (AC-9)', async () => {
    const s = server().on('POST /v1/transfers', () => transfer(5000, 45000));
    await renderApp('/send', { server: s, signedIn: true });
    const user = userEvent.setup();
    await user.click(await screen.findByLabelText('Recipient mobile number'));
    await user.keyboard('01112223333{Enter}');
    await screen.findByText('Send to Mona S. K.');
    expect(screen.getByTestId('recipient-preview')).toHaveFocus();
    await user.tab(); // Change
    await user.tab(); // Continue
    await user.keyboard('{Enter}');
    expect(await screen.findByLabelText('Amount')).toHaveFocus();
    await user.keyboard('50{Enter}');
    expect(await screen.findByRole('heading', { name: 'Check and send' })).toHaveFocus();
    await user.tab(); // Back
    await user.tab(); // Send
    expect(screen.getByRole('button', { name: 'Send' })).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(await screen.findByTestId('transfer-result')).toHaveFocus();
  });
});
