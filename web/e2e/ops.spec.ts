// T034: the operator console against the real /internal/v1 API of the compose stack.
import { expect, test, type Page } from '@playwright/test';
import { canSeedDatabase, deleteDiscrepancy, seedDiscrepancy } from './db';
import { e164, INTERNAL_API_KEY, newPhone, registerViaApi } from './helpers';
import { createUser, openAs } from './walletApi';

async function unlock(page: Page, path = '/ops') {
  await page.goto(path);
  await page.getByLabel('Operator key').fill(INTERNAL_API_KEY);
  await page.getByRole('button', { name: 'Unlock' }).click();
  await expect(page.getByRole('button', { name: 'Lock' })).toBeVisible();
}

test.describe('operator console', () => {
  test.describe.configure({ timeout: 90_000 });

  test('unlock: a wrong key is refused, the right one opens Health (AC-3, AC-12) @mobile', async ({ page, context }) => {
    await page.goto('/ops');
    await page.getByLabel('Operator key').fill('wrong-key');
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByText('Wrong operator key.')).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem('ewallet.ops.v1'))).toBeNull();

    await page.getByLabel('Operator key').fill(INTERNAL_API_KEY);
    await page.getByRole('button', { name: 'Unlock' }).click();
    await expect(page.getByRole('heading', { name: 'Health' })).toBeVisible();
    await expect(page.getByTestId('ops-status')).toHaveText(/All good|Needs attention|Critical/);
    await expect(page.getByRole('table', { name: 'Reconciliation jobs' })).toBeVisible();
    expect(await page.evaluate(() => document.querySelector('meta[name="robots"]')?.getAttribute('content'))).toBe('noindex');

    // Another tab must unlock on its own (sessionStorage is per tab).
    const other = await context.newPage();
    await other.goto('/ops');
    await expect(other.getByLabel('Operator key')).toBeVisible();
    await other.close();

    await page.getByRole('button', { name: 'Lock' }).click();
    await expect(page.getByText('Console locked.')).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem('ewallet.ops.v1'))).toBeNull();
    await page.reload();
    await expect(page.getByLabel('Operator key')).toBeVisible();
  });

  test('resolve a seeded discrepancy (AC-5)', async ({ page }) => {
    test.skip(!canSeedDatabase, 'needs the local compose database');
    const { id, marker } = seedDiscrepancy();
    try {
      await resolveFlow(page, id, marker);
    } finally {
      deleteDiscrepancy(id);
    }
  });

  async function resolveFlow(page: Page, id: string, marker: string) {
    await unlock(page, '/ops/discrepancies');
    await expect(page.getByTestId('discrepancy-row').first()).toBeVisible();
    await page.locator(`a[href="/ops/discrepancies/${id}"]`).click();
    await expect(page.getByTestId('discrepancy-details')).toContainText(marker);
    await expect(page.getByTestId('discrepancy-details')).toContainText('<b>not bold</b>');

    await page.getByRole('button', { name: 'Resolve' }).click();
    await expect(page.getByText('Enter a note of 1 to 500 characters.')).toBeVisible();
    await page.getByLabel('Resolution note').fill('Checked with the bank; e2e test.');
    await page.getByRole('button', { name: 'Resolve' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Resolve' }).click();
    await expect(page.getByText('Discrepancy resolved.')).toBeVisible();
    await expect(page.getByTestId('resolution-note')).toHaveText('Checked with the bank; e2e test.');

    // It is now in the Resolved list and not in the Open one; a direct load still finds it.
    await page.goto('/ops/discrepancies?status=RESOLVED');
    await expect(page.locator(`a[href="/ops/discrepancies/${id}"]`)).toBeVisible();
    await page.goto('/ops/discrepancies?status=OPEN');
    await expect(page.getByRole('heading', { name: 'Discrepancies' })).toBeVisible();
    await expect(page.locator(`a[href="/ops/discrepancies/${id}"]`)).toHaveCount(0);
    await page.goto(`/ops/discrepancies/${id}`);
    await expect(page.getByTestId('discrepancy-status')).toHaveText('Resolved');
  }

  test('freeze shows the banner to the user; unfreeze removes it (AC-7, AC-8)', async ({ page, browser, request }) => {
    const user = await createUser(request);
    const ctx = await browser.newContext();
    const userPage = await ctx.newPage();
    await openAs(userPage, user);
    const walletId = (await userPage.getByTestId('wallet-id').textContent())?.trim() ?? '';
    expect(walletId).toBe(user.walletId);

    await unlock(page, '/ops/wallets');
    await page.getByLabel('Wallet ID').fill('not-a-wallet');
    await page.getByRole('button', { name: 'Freeze', exact: true }).click();
    await expect(page.getByText(/Enter a wallet ID like/)).toBeVisible();
    await page.getByLabel('Wallet ID').fill('0193a0b2-0000-7000-8000-00000000dead');
    await page.getByRole('button', { name: 'Freeze', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Freeze', exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText('No wallet with this ID.');

    await page.getByLabel('Wallet ID').fill(walletId);
    await page.getByRole('button', { name: 'Freeze', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Freeze', exact: true }).click();
    await expect(page.getByText(`Wallet ${walletId} is now FROZEN.`)).toBeVisible();
    await userPage.reload();
    await expect(userPage.getByText(/Your wallet is frozen/)).toBeVisible();

    await page.getByRole('button', { name: 'Unfreeze' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Unfreeze' }).click();
    await expect(page.getByText(`Wallet ${walletId} is now ACTIVE.`)).toBeVisible();
    await userPage.reload();
    await expect(userPage.getByTestId('balance')).toBeVisible();
    await expect(userPage.getByText(/Your wallet is frozen/)).toHaveCount(0);
    await ctx.close();
  });

  test('reset code end to end; unknown phone is refused (AC-9)', async ({ page, browser, request }) => {
    const phone = newPhone();
    await registerViaApi(request, phone, 'old-password');
    await unlock(page, '/ops/reset-codes');

    await page.getByLabel('User mobile number').fill(newPhone());
    await page.getByRole('button', { name: 'Issue code' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Issue code' }).click();
    await expect(page.getByRole('alert')).toHaveText('No user with this phone number.');

    await page.getByLabel('User mobile number').fill(phone);
    await page.getByRole('button', { name: 'Issue code' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Issue code' }).click();
    const code = (await page.getByTestId('reset-code-value').textContent())?.trim() ?? '';
    expect(code).toMatch(/^\d{6}$/);
    await expect(page.getByText(/Expires at .* \(Cairo\)/)).toBeVisible();

    const ctx = await browser.newContext();
    const userPage = await ctx.newPage();
    await userPage.goto('/reset-password');
    await userPage.getByLabel('Mobile number').fill(phone);
    await userPage.getByLabel('Reset code').fill(code);
    await userPage.getByLabel('New password').fill('new-password');
    await userPage.getByLabel('Confirm password').fill('new-password');
    await userPage.getByRole('button', { name: 'Set new password' }).click();
    await expect(userPage.getByText('Password changed. Please log in.')).toBeVisible();
    await expect(userPage.getByLabel('Mobile number')).toHaveValue(e164(phone));
    await ctx.close();
  });
});
