// T033: send money between two real users through the UI.
import { expect, test, type Page } from '@playwright/test';
import { newPhone } from './helpers';
import { balanceOf, createUser, linkBank, openAs, setFrozen, topUp } from './walletApi';

async function toConfirm(page: Page, phone: string, amount: string, note?: string) {
  await page.getByLabel('Recipient mobile number').fill(phone);
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByTestId('recipient-preview')).toBeVisible();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByLabel('Amount').fill(amount);
  if (note) await page.getByLabel('Note (optional)').fill(note);
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Check and send' })).toBeVisible();
}

test.describe('send money', () => {
  test.describe.configure({ timeout: 90_000 });

  test('A sends EGP 50 with a note to B; both histories show it (AC-1) @mobile', async ({ page, browser, request }) => {
    const funded = await createUser(request, 'Ahmed Ali');
    const b = await createUser(request, 'Mona Samir Kamel');
    await linkBank(request, funded);
    await topUp(request, funded, 20_000);
    await openAs(page, funded, '/send');
    await page.getByLabel('Recipient mobile number').fill(b.phone);
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByTestId('recipient-preview')).toContainText('Send to Mona S. K.');
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('Available: EGP 200.00')).toBeVisible();
    await page.getByLabel('Amount').fill('50');
    await page.getByLabel('Note (optional)').fill('lunch');
    await page.getByRole('button', { name: 'Continue' }).click();
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.getByText('Sent EGP 50.00 to Mona S. K.')).toBeVisible();
    await expect(page.getByText('New balance: EGP 150.00')).toBeVisible();
    await page.getByRole('link', { name: 'View transaction' }).click();
    await expect(page.getByText('lunch')).toBeVisible();
    await page.goto('/history');
    await expect(page.getByText('Sent to Mona S. K.')).toBeVisible();

    const ctxB = await browser.newContext();
    const pageB = await ctxB.newPage();
    await openAs(pageB, b, '/history');
    await expect(pageB.getByText('Received from Ahmed A.')).toBeVisible();
    await expect(pageB.getByText('+EGP 50.00')).toBeVisible();
    await ctxB.close();
    expect(await balanceOf(request, b)).toBe(5_000);
  });

  test('unknown number and own number are refused at step 1 (AC-2)', async ({ page, request }) => {
    const a = await createUser(request);
    await openAs(page, a, '/send');
    const field = page.getByLabel('Recipient mobile number');
    await field.fill(newPhone()); // not registered
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText('No wallet uses this number.')).toBeVisible();
    await field.fill(a.phone);
    await page.getByRole('button', { name: 'Continue' }).click();
    await expect(page.getByText("You can't send money to yourself.")).toBeVisible();
    await expect(page.getByLabel('Amount')).toHaveCount(0);
  });

  test('double-clicking Send makes exactly one transfer (AC-8)', async ({ page, request }) => {
    const a = await createUser(request);
    const b = await createUser(request, 'Omar Nabil');
    await linkBank(request, a);
    await topUp(request, a, 10_000);
    await openAs(page, a, '/send');
    await toConfirm(page, b.phone, '25');
    await page.getByRole('button', { name: 'Send' }).dblclick();
    await expect(page.getByText('Sent EGP 25.00 to Omar N.')).toBeVisible();
    await page.goto('/history');
    await expect(page.getByText('Sent to Omar N.')).toHaveCount(1);
    expect(await balanceOf(request, a)).toBe(7_500);
    expect(await balanceOf(request, b)).toBe(2_500);
  });

  test('a frozen sender sees the banner instead of the flow (AC-7)', async ({ page, request }) => {
    const a = await createUser(request);
    await setFrozen(request, a.walletId, true);
    await openAs(page, a, '/send');
    await expect(page.getByText(/Your wallet is frozen/)).toBeVisible();
    await expect(page.getByLabel('Recipient mobile number')).toHaveCount(0);
  });
});
