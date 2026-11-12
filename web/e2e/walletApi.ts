// API-level setup for the wallet screens' browser tests (T031+): create users, fund them and
// move money without going through the UI under test.
import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { e164, INTERNAL_API_KEY, newPhone, registerViaApi, STORAGE_KEY } from './helpers';

/** Bank simulator scenarios are chosen by the IBAN's last four digits (TDR-008 §3). */
export const IBAN_SUCCESS = 'EG890003000100000000123450000';
export const IBAN_FAIL_LATER = 'EG620003000100000000123450001';

export interface ApiUser {
  phone: string; // local format, as typed in the UI
  token: string;
  refreshToken: string;
  walletId: string;
}

/**
 * Logs in through the API. The login limit is per client IP (30/min, T004) and every test runs
 * from one IP, so a 429 waits and tries again (up to ~50 s; specs using this set a 90 s timeout).
 */
async function apiLogin(request: APIRequestContext, phone: string) {
  for (let attempt = 0; ; attempt++) {
    const res = await request.post('/v1/auth/login', { data: { phone: e164(phone), password: 'secret123' } });
    if (res.status() === 429 && attempt < 10) {
      await new Promise((r) => setTimeout(r, 5_000));
      continue;
    }
    expect(res.status(), await res.text()).toBe(200);
    return (await res.json()) as { access_token: string; refresh_token: string };
  }
}

export async function createUser(request: APIRequestContext, fullName = 'E2E User'): Promise<ApiUser> {
  const phone = newPhone();
  const reg = await request.post('/v1/auth/register', {
    data: { phone: e164(phone), full_name: fullName, password: 'secret123' },
  });
  expect(reg.status(), await reg.text()).toBe(201);
  const { wallet_id } = (await reg.json()) as { wallet_id: string };
  const { access_token, refresh_token } = await apiLogin(request, phone);
  return { phone, token: access_token, refreshToken: refresh_token, walletId: wallet_id };
}

/**
 * Opens the app already signed in as `user`, by seeding the stored refresh token the way the app
 * stores it (TDR-020 §1); the app restores the session with a refresh, so no extra login is
 * spent. Seeding happens once per tab, so a later sign-out sticks.
 */
export async function openAs(page: Page, user: ApiUser, path = '/') {
  await page.addInitScript(
    ([key, token]) => {
      if (window.sessionStorage.getItem('e2e.seeded')) return;
      window.sessionStorage.setItem('e2e.seeded', '1');
      window.localStorage.setItem(key, JSON.stringify({ refresh_token: token, refresh_expires_at: Date.now() + 86_400_000 }));
    },
    [STORAGE_KEY, user.refreshToken] as const,
  );
  await page.goto(path);
}

export { registerViaApi };

function auth(user: ApiUser, extra: Record<string, string> = {}) {
  return { Authorization: `Bearer ${user.token}`, ...extra };
}

export async function linkBank(request: APIRequestContext, user: ApiUser, iban = IBAN_SUCCESS) {
  const res = await request.put('/v1/me/bank-account', { headers: auth(user), data: { iban, holder_name: 'E2E User' } });
  expect(res.status(), await res.text()).toBe(200);
}

export async function waitFinal(request: APIRequestContext, user: ApiUser, id: string): Promise<string> {
  let status = '';
  await expect
    .poll(
      async () => {
        const res = await request.get(`/v1/transactions/${id}`, { headers: auth(user) });
        status = ((await res.json()) as { status: string }).status;
        return status;
      },
      { timeout: 30_000, intervals: [500] },
    )
    .toMatch(/COMPLETED|FAILED/);
  return status;
}

/** Tops up through the bank simulator and waits for the bank's answer. */
export async function topUp(request: APIRequestContext, user: ApiUser, amountMinor: number, wait = true) {
  const res = await request.post('/v1/topups', {
    headers: auth(user, { 'Idempotency-Key': randomUUID() }),
    data: { amount_minor: amountMinor, currency: 'EGP' },
  });
  expect(res.status(), await res.text()).toBe(202);
  const { transaction_id } = (await res.json()) as { transaction_id: string };
  if (wait) expect(await waitFinal(request, user, transaction_id)).toBe('COMPLETED');
  return transaction_id;
}

export async function transfer(request: APIRequestContext, from: ApiUser, to: ApiUser, amountMinor: number, note?: string) {
  const res = await request.post('/v1/transfers', {
    headers: auth(from, { 'Idempotency-Key': randomUUID() }),
    data: { recipient_phone: e164(to.phone), amount_minor: amountMinor, currency: 'EGP', ...(note ? { note } : {}) },
  });
  expect(res.status(), await res.text()).toBe(201);
  return ((await res.json()) as { transaction_id: string }).transaction_id;
}

export async function setFrozen(request: APIRequestContext, walletId: string, frozen: boolean) {
  const res = await request.post(`/internal/v1/wallets/${walletId}/${frozen ? 'freeze' : 'unfreeze'}`, {
    headers: { 'X-Internal-Api-Key': INTERNAL_API_KEY },
  });
  expect(res.status(), await res.text()).toBe(200);
}
