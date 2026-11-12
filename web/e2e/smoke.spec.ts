import { expect, test } from '@playwright/test';

// Safe to run against a deployed app (WEB_BASE_URL): creates nothing.
test.describe('smoke', () => {
  test('the app loads with its CSP and no console errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (m) => {
      if (m.type() === 'error') errors.push(m.text());
    });
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Log in' })).toBeVisible();
    const csp = await page.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute('content');
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("script-src 'self'");
    expect(errors).toEqual([]);
  });

  test('a deep link to a client route loads the app', async ({ page }) => {
    await page.goto('/register');
    await expect(page.getByRole('heading', { name: 'Create your wallet' })).toBeVisible();
    await page.goto('/reset-password');
    await expect(page.getByRole('heading', { name: 'Reset your password' })).toBeVisible();
  });

  test('the API is reachable on the same origin', async ({ request }) => {
    const res = await request.post('/v1/auth/login', { data: { phone: '+201000000000', password: 'nope-nope' } });
    expect([401, 429]).toContain(res.status());
  });
});
