import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { renderApp } from '@/test/renderApp';
import { OPS_STORAGE_KEY } from './opsKey';

const D1 = '0193a0b2-0000-7000-8000-0000000000d1';
const WALLET = '0193a0b2-0000-7000-8000-000000000001';

function health(over: Record<string, unknown> = {}) {
  return {
    status: 'warning',
    discrepancies: { open_critical: 0, open_warning: 2 },
    stuck: { TOPUP: 1 },
    outbox: { pending: 0, dead: 0 },
    inbox: {},
    jobs: [
      { name: 'stuck-transactions', last_status: 'SUCCESS', last_finished_at: '2026-11-12T12:00:00Z', late: false },
      { name: 'bank-statement', last_status: null, last_finished_at: null, late: true },
    ],
    ...over,
  };
}

function discrepancy(over: Record<string, unknown> = {}) {
  return {
    id: D1,
    kind: 'STATUS_MISMATCH',
    severity: 'WARNING',
    transaction_id: '0193a0b2-0000-7000-8000-0000000000f1',
    details: { ours: 'PENDING', bank: 'SUCCEEDED' },
    status: 'OPEN',
    created_at: '2026-11-12T12:00:00Z',
    resolved_at: null,
    resolution_note: null,
    ...over,
  };
}

function server() {
  return new FakeServer()
    .on('GET /internal/v1/health/ops', () => json(200, health()))
    .on('GET /internal/v1/discrepancies', () => json(200, { items: [discrepancy()], next_cursor: null }));
}

async function open(path: string, s: FakeServer, unlocked = true) {
  vi.stubGlobal('fetch', s.fetch);
  if (unlocked) window.sessionStorage.setItem(OPS_STORAGE_KEY, JSON.stringify({ key: 'ops-key', lastActive: Date.now() }));
  return renderApp(path, { server: s });
}

beforeEach(() => window.sessionStorage.clear());
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('Operator console: unlock and lock (T034)', () => {
  it('a right key is checked against health, stored, and opens Health (AC-3)', async () => {
    const s = server();
    await open('/ops', s, false);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Operator key'), 'ops-key');
    await user.click(screen.getByRole('button', { name: 'Unlock' }));
    expect(await screen.findByRole('heading', { name: 'Health' })).toBeInTheDocument();
    expect(screen.getByTestId('ops-status')).toHaveTextContent('Needs attention');
    expect(s.fetch.mock.calls[0]?.[0]).toBeInstanceOf(Request);
    expect((s.fetch.mock.calls[0]?.[0] as Request).headers.get('X-Internal-Api-Key')).toBe('ops-key');
    expect(JSON.parse(window.sessionStorage.getItem(OPS_STORAGE_KEY) ?? '{}').key).toBe('ops-key');
  });

  it('a wrong key shows "Wrong operator key." and stores nothing (AC-3)', async () => {
    const s = server().on('GET /internal/v1/health/ops', () => apiError(401, 'UNAUTHENTICATED'));
    await open('/ops', s, false);
    const user = userEvent.setup();
    const field = await screen.findByLabelText('Operator key');
    await user.type(field, 'nope');
    await user.click(screen.getByRole('button', { name: 'Unlock' }));
    await waitFor(() => expect(field).toHaveAccessibleDescription('Wrong operator key.'));
    expect(window.sessionStorage.getItem(OPS_STORAGE_KEY)).toBeNull();
  });

  it('404 on unlock: the operator API is not enabled (AC-4)', async () => {
    const s = server().on('GET /internal/v1/health/ops', () => json(404, { error: { code: 'NOT_FOUND', message: 'x' } }));
    await open('/ops', s, false);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Operator key'), 'k');
    await user.click(screen.getByRole('button', { name: 'Unlock' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('The operator API is not enabled on this server.');
  });

  it('Lock returns to the unlock screen and clears the key (AC-12)', async () => {
    const s = server();
    await open('/ops', s);
    await screen.findByTestId('ops-status');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Lock' }));
    expect(await screen.findByLabelText('Operator key')).toBeInTheDocument();
    expect(screen.getByText('Console locked.')).toBeInTheDocument();
    expect(window.sessionStorage.getItem(OPS_STORAGE_KEY)).toBeNull();
  });

  it('a 401 later locks with "The operator key was rejected."', async () => {
    let ok = true;
    const s = server().on('GET /internal/v1/health/ops', () => (ok ? json(200, health()) : apiError(401, 'UNAUTHENTICATED')));
    await open('/ops', s);
    await screen.findByTestId('ops-status');
    ok = false;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText('The operator key was rejected. Unlock again.')).toBeInTheDocument();
  });

  it('the user session is not needed and not touched', async () => {
    const s = server();
    await open('/ops', s);
    await screen.findByTestId('ops-status');
    expect(s.calls.every((c) => c.path.startsWith('/internal/') && c.auth === null)).toBe(true);
  });
});

describe('Operator console: health (T034)', () => {
  it('shows status in words, counts, jobs with a Late badge, and links to open discrepancies (AC-14)', async () => {
    await open('/ops', server());
    expect(await screen.findByTestId('ops-status')).toHaveTextContent('Needs attention');
    const jobs = screen.getByRole('table', { name: 'Reconciliation jobs' });
    expect(within(jobs).getByText('bank-statement').closest('tr')).toHaveTextContent('Late');
    expect(within(jobs).getByText('stuck-transactions').closest('tr')).toHaveTextContent('12 Nov 2026, 14:00');
    expect(within(screen.getByRole('region', { name: 'Stuck transactions' })).getByText('1')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View open discrepancies' })).toHaveAttribute('href', '/ops/discrepancies?status=OPEN');
  });

  it('a failed refresh says the figures may be out of date', async () => {
    let fail = false;
    const s = server().on('GET /internal/v1/health/ops', () => (fail ? apiError(500, 'INTERNAL_ERROR') : json(200, health())));
    await open('/ops', s);
    await screen.findByTestId('ops-status');
    fail = true;
    await userEvent.setup().click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText(/Couldn't refresh/, {}, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByTestId('ops-status')).toBeInTheDocument();
  });

  it('refreshes every 30 s and with the Refresh button (AC-13)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const s = server();
    await open('/ops', s);
    await screen.findByTestId('ops-status');
    const n = s.count('GET /internal/v1/health/ops');
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(s.count('GET /internal/v1/health/ops')).toBe(n + 1);
    await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(s.count('GET /internal/v1/health/ops')).toBe(n + 2));
  });
});

describe('Operator console: discrepancies (T034)', () => {
  it('lists open items, shows the details and resolves with a note (AC-5)', async () => {
    const s = server().on(`POST /internal/v1/discrepancies/${D1}/resolve`, (_r, body) =>
      json(200, discrepancy({ status: 'RESOLVED', resolved_at: '2026-11-12T13:00:00Z', resolution_note: (body as { note: string }).note })),
    );
    await open('/ops/discrepancies', s);
    const user = userEvent.setup();
    await screen.findAllByTestId('discrepancy-row');
    expect(new URL((s.fetch.mock.calls.at(-1)?.[0] as Request).url).searchParams.get('status')).toBe('OPEN');
    await user.click(screen.getByRole('link', { name: /View/ }));
    expect(await screen.findByTestId('discrepancy-details')).toHaveTextContent('"bank": "SUCCEEDED"');
    // Empty note: rejected with no request.
    await user.click(screen.getByRole('button', { name: 'Resolve' }));
    expect(screen.getByLabelText('Resolution note')).toHaveAccessibleDescription(/Enter a note of 1 to 500 characters\./);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Resolution note'), '  Bank confirmed; ledger fixed by T026 job. ');
    await user.click(screen.getByRole('button', { name: 'Resolve' }));
    const dialog = screen.getByRole('dialog', { name: 'Resolve this discrepancy?' });
    expect(dialog).toHaveTextContent('This only records a note; no money moves.');
    await user.click(within(dialog).getByRole('button', { name: 'Resolve' }));
    expect(await screen.findByText('Discrepancy resolved.')).toBeInTheDocument();
    expect(screen.getByTestId('discrepancy-status')).toHaveTextContent('Resolved');
    expect(screen.getByTestId('resolution-note')).toHaveTextContent('Bank confirmed; ledger fixed by T026 job.');
    expect(s.calls.find((c) => c.method === 'POST')?.body).toEqual({ note: 'Bank confirmed; ledger fixed by T026 job.' });
  });

  it('409 ALREADY_RESOLVED reloads the item and says "Someone already resolved this discrepancy." (AC-6)', async () => {
    let resolved = false;
    const s = server()
      .on('GET /internal/v1/discrepancies', () =>
        json(200, { items: [resolved ? discrepancy({ status: 'RESOLVED', resolved_at: '2026-11-12T12:30:00Z', resolution_note: 'by someone else' }) : discrepancy()], next_cursor: null }),
      )
      .on(`POST /internal/v1/discrepancies/${D1}/resolve`, () => {
        resolved = true;
        return apiError(409, 'ALREADY_RESOLVED');
      });
    await open(`/ops/discrepancies/${D1}`, s);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Resolution note'), 'done');
    await user.click(screen.getByRole('button', { name: 'Resolve' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Resolve' }));
    expect(await screen.findByText('Someone already resolved this discrepancy.')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('discrepancy-status')).toHaveTextContent('Resolved'));
    expect(screen.getByText('Someone already resolved this discrepancy.')).toBeInTheDocument();
    expect(screen.getByTestId('resolution-note')).toHaveTextContent('by someone else');
    expect(screen.queryByLabelText('Resolution note')).not.toBeInTheDocument();
  });

  it('details with <script> render as text (AC-10)', async () => {
    const s = server().on('GET /internal/v1/discrepancies', () =>
      json(200, { items: [discrepancy({ details: { note: '<script>alert(1)</script><b>x</b>' } })], next_cursor: null }),
    );
    await open(`/ops/discrepancies/${D1}`, s);
    const pre = await screen.findByTestId('discrepancy-details');
    expect(pre).toHaveTextContent('<script>alert(1)</script><b>x</b>');
    expect(pre.querySelector('script, b')).toBeNull();
  });

  it('a direct load pages through the list; unknown ID → "Discrepancy not found."', async () => {
    let page = 0;
    const s = server().on('GET /internal/v1/discrepancies', () => {
      page++;
      return json(200, { items: [discrepancy({ id: `0193a0b2-0000-7000-8000-00000000000${page}` })], next_cursor: page < 3 ? `c${page}` : null });
    });
    await open('/ops/discrepancies/0193a0b2-0000-7000-8000-0000000000ff', s);
    expect(await screen.findByText('Discrepancy not found.')).toBeInTheDocument();
    expect(s.count('GET /internal/v1/discrepancies')).toBe(3);
  });

  it('filters live in the URL', async () => {
    const s = server();
    const { router } = await open('/ops/discrepancies', s);
    const user = userEvent.setup();
    await screen.findAllByTestId('discrepancy-row');
    await user.selectOptions(screen.getByLabelText('Kind'), 'AMOUNT_MISMATCH');
    await user.selectOptions(screen.getByLabelText('Status'), 'ALL');
    await waitFor(() => expect(router.state.location.search).toBe('?kind=AMOUNT_MISMATCH&status=ALL'));
    const url = new URL((s.fetch.mock.calls.at(-1)?.[0] as Request).url);
    expect(url.searchParams.get('kind')).toBe('AMOUNT_MISMATCH');
    expect(url.searchParams.has('status')).toBe(false);
  });
});

describe('Operator console: wallets and reset codes (T034)', () => {
  it('invalid UUID is rejected in the form; unknown → "No wallet with this ID." (AC-8)', async () => {
    const s = server().on(`POST /internal/v1/wallets/${WALLET}/freeze`, () => apiError(404, 'WALLET_NOT_FOUND'));
    await open('/ops/wallets', s);
    const user = userEvent.setup();
    const field = await screen.findByLabelText('Wallet ID');
    await user.type(field, 'not-a-uuid');
    await user.click(screen.getByRole('button', { name: 'Freeze' }));
    expect(field).toHaveAccessibleDescription(/Enter a wallet ID like/);
    expect(s.count(`POST /internal/v1/wallets/${WALLET}/freeze`)).toBe(0);
    await user.clear(field);
    await user.type(field, WALLET.toUpperCase());
    await user.keyboard('{Enter}');
    const dialog = screen.getByRole('dialog', { name: 'Freeze wallet?' });
    expect(dialog).toHaveTextContent(`Freeze wallet ${WALLET}? The user can't top up, withdraw or send until it is unfrozen.`);
    await user.click(within(dialog).getByRole('button', { name: 'Freeze' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('No wallet with this ID.');
  });

  it('unfreeze shows the new status', async () => {
    const s = server().on(`POST /internal/v1/wallets/${WALLET}/unfreeze`, () => json(200, { wallet_id: WALLET, status: 'ACTIVE' }));
    await open('/ops/wallets', s);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Wallet ID'), WALLET);
    await user.click(screen.getByRole('button', { name: 'Unfreeze' }));
    await user.click(within(screen.getByRole('dialog', { name: 'Unfreeze wallet?' })).getByRole('button', { name: 'Unfreeze' }));
    expect(await screen.findByText(`Wallet ${WALLET} is now ACTIVE.`)).toBeInTheDocument();
  });

  it('issues a reset code for a local-format phone; the path escapes "+" (UI-BR-35)', async () => {
    const s = server().on('POST /internal/v1/users/%2B201001234567/password-reset-code', () =>
      json(201, { phone: '+201001234567', code: '048213', expires_at: '2026-11-12T12:20:00Z' }),
    );
    await open('/ops/reset-codes', s);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('User mobile number'), '0100 123 4567');
    await user.click(screen.getByRole('button', { name: 'Issue code' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Issue code' }));
    expect(await screen.findByTestId('reset-code-value')).toHaveTextContent('048213');
    expect(screen.getByText('Code: 0 4 8 2 1 3')).toBeInTheDocument();
    expect(screen.getByText('Expires at 12 Nov 2026, 14:20 (Cairo)')).toBeInTheDocument();
    const req = s.fetch.mock.calls.map(([r]) => r as Request).find((r) => r.method === 'POST');
    expect(req?.url).toContain('/internal/v1/users/%2B201001234567/password-reset-code');
  });

  it('unknown phone → "No user with this phone number."', async () => {
    const s = server().on('POST /internal/v1/users/%2B201001234567/password-reset-code', () => apiError(404, 'USER_NOT_FOUND'));
    await open('/ops/reset-codes', s);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('User mobile number'), '01001234567');
    await user.click(screen.getByRole('button', { name: 'Issue code' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Issue code' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('No user with this phone number.');
  });

  it('nav and every form work from the keyboard (AC-14)', async () => {
    const s = server().on(`POST /internal/v1/wallets/${WALLET}/freeze`, () => json(200, { wallet_id: WALLET, status: 'FROZEN' }));
    await open('/ops', s);
    const user = userEvent.setup();
    await screen.findByTestId('ops-status');
    const nav = screen.getByRole('navigation', { name: 'Operator' });
    within(nav).getByRole('link', { name: 'Wallets' }).focus();
    await user.keyboard('{Enter}');
    await user.click(await screen.findByLabelText('Wallet ID'));
    await user.keyboard(`${WALLET}{Enter}`);
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Freeze' })).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(await screen.findByText(`Wallet ${WALLET} is now FROZEN.`)).toBeInTheDocument();
  });
});
