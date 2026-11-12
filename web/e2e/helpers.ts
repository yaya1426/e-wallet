import { expect, type APIRequestContext, type Page } from '@playwright/test';

/** Operator key of the local compose stack (docker-compose.yml, DEV ONLY). */
export const INTERNAL_API_KEY = process.env.INTERNAL_API_KEY ?? 'dev-only-internal-api-key-0123456789abcdef';
export const STORAGE_KEY = 'ewallet.session.v1';

/** A fresh, valid Egyptian mobile number in local format (010 + 8 random digits). */
export function newPhone(): string {
  return `010${String(Math.floor(Math.random() * 1e8)).padStart(8, '0')}`;
}

export function e164(local: string): string {
  return `+2${local}`;
}

export async function registerViaApi(request: APIRequestContext, phone: string, password = 'secret123') {
  const res = await request.post('/v1/auth/register', {
    data: { phone: e164(phone), full_name: 'E2E User', password },
  });
  expect(res.status(), await res.text()).toBe(201);
}

export async function loginViaUi(page: Page, phone: string, password = 'secret123') {
  await page.goto('/login');
  await page.getByLabel('Mobile number').fill(phone);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
}

export async function storedRefreshToken(page: Page): Promise<string | null> {
  const raw = await page.evaluate((key) => window.localStorage.getItem(key), STORAGE_KEY);
  return raw ? (JSON.parse(raw) as { refresh_token: string }).refresh_token : null;
}
