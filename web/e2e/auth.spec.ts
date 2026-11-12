import { expect, test } from '@playwright/test';
import { e164, INTERNAL_API_KEY, loginViaUi, newPhone, registerViaApi, storedRefreshToken } from './helpers';

test.describe('auth flows against the real API', () => {
  test('sign up lands on home with a zero balance (AC-5) @mobile', async ({ page }) => {
    await page.goto('/register');
    await page.getByLabel('Mobile number').fill(newPhone());
    await page.getByLabel('Full name').fill('Ahmed Ali');
    await page.getByLabel('Password', { exact: true }).fill('secret123');
    await page.getByLabel('Confirm password').fill('secret123');
    await page.getByRole('button', { name: 'Sign up' }).click();
    await expect(page.getByRole('heading', { name: 'My wallet' })).toBeVisible();
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
    await expect(page).toHaveURL(/\/$/);
  });

  test('a registered phone cannot sign up again (AC-6)', async ({ page, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone);
    await page.goto('/register');
    await page.getByLabel('Mobile number').fill(phone);
    await page.getByLabel('Full name').fill('Someone Else');
    await page.getByLabel('Password', { exact: true }).fill('secret123');
    await page.getByLabel('Confirm password').fill('secret123');
    await page.getByRole('button', { name: 'Sign up' }).click();
    await expect(page.getByText('This phone number is already registered. Log in instead.')).toBeVisible();
    await page.getByRole('link', { name: 'Log in instead' }).click();
    await expect(page.getByLabel('Mobile number')).toHaveValue(e164(phone));
  });

  test('login works and a wrong password is refused (AC-8) @mobile', async ({ page, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone);
    await page.goto('/login');
    await page.getByLabel('Mobile number').fill(phone);
    await page.getByLabel('Password', { exact: true }).fill('wrong-password');
    await page.getByRole('button', { name: 'Log in' }).click();
    await expect(page.getByRole('alert')).toHaveText('Wrong phone number or password.');
    await loginViaUi(page, phone);
  });

  test('a reload keeps the user signed in (AC-9)', async ({ page, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone);
    await loginViaUi(page, phone);
    const before = await storedRefreshToken(page);
    await page.reload();
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
    const after = await storedRefreshToken(page);
    expect(after).not.toBeNull();
    expect(after).not.toBe(before); // rotated on restore
  });

  test('two tabs reloading at the same time both stay signed in (AC-11)', async ({ page, context, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone);
    await loginViaUi(page, phone);
    const second = await context.newPage();
    await second.goto('/');
    await expect(second.getByTestId('balance')).toHaveText('EGP 0.00');

    for (let i = 0; i < 3; i++) {
      await Promise.all([page.reload(), second.reload()]);
      await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
      await expect(second.getByTestId('balance')).toHaveText('EGP 0.00');
    }
    // The session is still alive on the server: a third tab can restore it.
    const third = await context.newPage();
    await third.goto('/');
    await expect(third.getByTestId('balance')).toHaveText('EGP 0.00');
  });

  test('sign out ends the session in every tab and on the server (AC-14)', async ({ page, context, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone);
    await loginViaUi(page, phone);
    const second = await context.newPage();
    await second.goto('/');
    await expect(second.getByTestId('balance')).toBeVisible();
    const token = await storedRefreshToken(page);
    expect(token).not.toBeNull();

    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page.getByText('You have signed out.')).toBeVisible();
    await expect(second.getByRole('heading', { name: 'Log in' })).toBeVisible();
    expect(await storedRefreshToken(page)).toBeNull();

    const res = await request.post('/v1/auth/refresh', { data: { refresh_token: token } });
    expect(res.status()).toBe(401);
    expect((await res.json()).error.code).toBe('INVALID_REFRESH_TOKEN');

    await page.goto('/');
    await expect(page).toHaveURL(/\/login\?next=%2F$/);
  });

  test('password reset with an operator code (AC-15)', async ({ page, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone, 'old-password');

    // Wrong code first.
    await page.goto('/reset-password');
    await page.getByLabel('Mobile number').fill(phone);
    await page.getByLabel('Reset code').fill('000000');
    await page.getByLabel('New password').fill('new-password');
    await page.getByLabel('Confirm password').fill('new-password');
    await page.getByRole('button', { name: 'Set new password' }).click();
    await expect(page.getByRole('alert')).toHaveText('The code is wrong or expired. Ask support for a new one.');

    const issued = await request.post(`/internal/v1/users/${encodeURIComponent(e164(phone))}/password-reset-code`, {
      headers: { 'X-Internal-Api-Key': INTERNAL_API_KEY },
    });
    expect(issued.status(), await issued.text()).toBe(201);
    const { code } = (await issued.json()) as { code: string };

    await page.getByLabel('Reset code').fill(code);
    await page.getByRole('button', { name: 'Set new password' }).click();
    await expect(page.getByText('Password changed. Please log in.')).toBeVisible();
    await expect(page.getByLabel('Mobile number')).toHaveValue(e164(phone));

    await page.getByLabel('Password', { exact: true }).fill('old-password');
    await page.getByRole('button', { name: 'Log in' }).click();
    await expect(page.getByRole('alert')).toHaveText('Wrong phone number or password.');
    await page.getByLabel('Password', { exact: true }).fill('new-password');
    await page.getByRole('button', { name: 'Log in' }).click();
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
  });
});
