// T032: bank account, top-up and withdrawal against the real API and the bank simulator.
import { expect, test, type Page } from '@playwright/test';
import { balanceOf, createUser, IBAN_FAIL_LATER, IBAN_SUCCESS, linkBank, openAs, setFrozen, topUp } from './walletApi';

async function topUpViaUi(page: Page, amount: string) {
  await page.goto('/top-up');
  await page.getByLabel('Amount').fill(amount);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click();
}

async function withdrawViaUi(page: Page, amount: string) {
  await page.goto('/withdraw');
  await page.getByLabel('Amount').fill(amount);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click();
}

test.describe('bank account, top-up and withdrawal', () => {
  test.describe.configure({ timeout: 90_000 });

  test('link an account, with an invalid IBAN refused first (AC-2, AC-4) @mobile', async ({ page, request }) => {
    const user = await createUser(request);
    await openAs(page, user, '/withdraw');
    await expect(page.getByText('Link a bank account first.')).toBeVisible();
    await page.goto('/top-up');
    await expect(page.getByText('Link a bank account first.')).toBeVisible();
    await page.getByRole('link', { name: 'Link a bank account' }).click();
    let puts = 0;
    page.on('request', (r) => {
      if (r.method() === 'PUT' && r.url().endsWith('/v1/me/bank-account')) puts++;
    });
    await page.getByRole('button', { name: 'Link bank account' }).click();
    await page.getByLabel('IBAN').fill('EG880003000100000000123450000');
    await page.getByLabel('Account holder name').fill('Ahmed Ali');
    await page.getByRole('button', { name: 'Save bank account' }).click();
    await expect(page.getByText(/Enter a valid Egyptian IBAN/)).toBeVisible();
    expect(puts).toBe(0); // refused in the browser
    await page.getByLabel('IBAN').fill('eg89 0003 0001 0000 0000 1234 5000 0');
    await page.getByRole('button', { name: 'Save bank account' }).click();
    await expect(page.getByText('Bank account saved.')).toBeVisible();
    await expect(page.getByTestId('bank-account')).toContainText('EG89 0003 0001 0000 0000 1234 5000 0');
    await page.reload();
    await expect(page.getByTestId('bank-account')).toContainText('Ahmed Ali');
  });

  test('top-up succeeds and the balance goes up (AC-5) @mobile', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_SUCCESS);
    await openAs(page, user);
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
    await topUpViaUi(page, '500');
    await expect(page.getByText('Waiting for your bank…')).toBeVisible();
    await expect(page.getByText('Top-up complete')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('New balance: EGP 500.00')).toBeVisible();
    await page.getByRole('link', { name: 'Back to home' }).click();
    await expect(page.getByTestId('balance')).toHaveText('EGP 500.00');
  });

  test('top-up the bank refuses later ends FAILED, balance unchanged (AC-6)', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_FAIL_LATER);
    await openAs(page, user);
    await topUpViaUi(page, '100');
    await expect(page.getByText("Your bank account didn't have enough money.")).toBeVisible({ timeout: 20_000 });
    await page.goto('/');
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
  });

  test('withdrawal reserves at once and completes (AC-7)', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_SUCCESS);
    await topUp(request, user, 50_000);
    await openAs(page, user);
    await expect(page.getByTestId('balance')).toHaveText('EGP 500.00');
    await withdrawViaUi(page, '200');
    await expect(page.getByText('Waiting for your bank…')).toBeVisible();
    // Reserved at once, while the bank has not answered yet (PRD-003).
    expect(await balanceOf(request, user)).toBe(30_000);
    await expect(page.getByText('Withdrawal complete')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('New balance: EGP 300.00')).toBeVisible();
  });

  test('withdrawal the bank refuses is refunded (AC-8)', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_SUCCESS);
    await topUp(request, user, 50_000);
    await linkBank(request, user, IBAN_FAIL_LATER);
    await openAs(page, user);
    await withdrawViaUi(page, '200');
    await expect(page.getByText(/The bank account is closed\. The money is back in your wallet\./)).toBeVisible({ timeout: 20_000 });
    await page.goto('/');
    await expect(page.getByTestId('balance')).toHaveText('EGP 500.00');
  });

  test('withdrawing more than the balance is refused (AC-11)', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_SUCCESS);
    await topUp(request, user, 5_000);
    await openAs(page, user);
    await withdrawViaUi(page, '100');
    await expect(page.getByRole('alert')).toHaveText("You don't have enough balance.");
  });

  test('a frozen wallet cannot top up (AC-12)', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user, IBAN_SUCCESS);
    await setFrozen(request, user.walletId, true);
    await openAs(page, user, '/top-up');
    await expect(page.getByText(/Your wallet is frozen/)).toBeVisible();
    await expect(page.getByLabel('Amount')).toHaveCount(0);
  });
});
