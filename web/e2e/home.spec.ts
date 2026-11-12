// T031: wallet home, history and transaction detail against the real API.
import { expect, test, type Page } from '@playwright/test';
import { e164 } from './helpers';
import { createUser, linkBank, openAs, setFrozen, topUp, transfer, type ApiUser } from './walletApi';

async function loginAs(page: Page, user: ApiUser) {
  await openAs(page, user);
  await expect(page.getByRole('heading', { name: 'My wallet' })).toBeVisible();
}

test.describe('wallet home and history', () => {
  // Users are created through the API, which may wait out the per-IP login limit.
  test.describe.configure({ timeout: 90_000 });

  test('a top-up shows on home with the new balance and wallet ID (AC-1, AC-10) @mobile', async ({ page, request }) => {
    const user = await createUser(request);
    await linkBank(request, user);
    await loginAs(page, user);
    await expect(page.getByTestId('balance')).toHaveText('EGP 0.00');
    await expect(page.getByTestId('wallet-id')).toHaveText(user.walletId);
    await expect(page.getByText('No transactions yet.')).toBeVisible();

    await topUp(request, user, 50_000);
    // Coming back to the tab refetches without a reload (UI-BR-19).
    await page.evaluate(() => window.dispatchEvent(new Event('visibilitychange')));
    await expect(page.getByTestId('balance')).toHaveText('EGP 500.00');
    const row = page.getByRole('main').getByRole('link', { name: /Top-up/ });
    await expect(row).toContainText('+EGP 500.00');
    await expect(row).toContainText('Completed');
    await row.click();
    await expect(page.getByRole('heading', { name: 'Transaction' })).toBeVisible();
    await expect(page.getByTestId('detail-amount')).toHaveText('+EGP 500.00');
  });

  test('a P2P transfer shows on both sides with masked names (AC-9)', async ({ browser, request }) => {
    const sender = await createUser(request, 'Ahmed Mohamed Ali');
    const receiver = await createUser(request, 'Mona Hassan Saad');
    await linkBank(request, sender);
    await topUp(request, sender, 10_000);
    await transfer(request, sender, receiver, 2_500, 'lunch');

    const ctxA = await browser.newContext();
    const ctxB = await browser.newContext();
    const a = await ctxA.newPage();
    await loginAs(a, sender);
    await expect(a.getByTestId('balance')).toHaveText('EGP 75.00');
    const sent = a.getByRole('main').getByRole('link', { name: /Sent to Mona H\. S\./ });
    await expect(sent).toContainText('−EGP 25.00');

    const b = await ctxB.newPage();
    await loginAs(b, receiver);
    const received = b.getByRole('main').getByRole('link', { name: /Received from Ahmed M\. A\./ });
    await expect(received).toContainText('+EGP 25.00');
    await received.click();
    await expect(b.getByText('lunch')).toBeVisible();
    await expect(b.getByText(`Ahmed M. A. (+20 ${e164(sender.phone).slice(3, 6)}`)).toBeVisible();
    await ctxA.close();
    await ctxB.close();
  });

  test('history pages 20 at a time (AC-5)', async ({ page, request }) => {
    const sender = await createUser(request);
    const receiver = await createUser(request);
    await linkBank(request, sender);
    await topUp(request, sender, 10_000);
    for (let i = 0; i < 25; i++) await transfer(request, sender, receiver, 100);

    await loginAs(page, receiver);
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'History' }).click();
    const rows = page.getByRole('main').getByRole('listitem');
    await expect(rows).toHaveCount(20);
    await page.getByRole('button', { name: 'Load more' }).click();
    await expect(rows).toHaveCount(25);
    await expect(page.getByRole('button', { name: 'Load more' })).toHaveCount(0);
  });

  test('frozen wallet shows the banner and disables actions (AC-2)', async ({ page, request }) => {
    const user = await createUser(request);
    await setFrozen(request, user.walletId, true);
    await loginAs(page, user);
    await expect(page.getByText(/Your wallet is frozen/)).toBeVisible();
    await expect(page.getByRole('main').getByRole('button', { name: 'Top up' })).toHaveAttribute('aria-disabled', 'true');
    await setFrozen(request, user.walletId, false);
    await page.reload();
    await expect(page.getByRole('link', { name: 'Top up' })).toHaveAttribute('href', '/top-up');
  });

  test('an unknown transaction shows "Transaction not found." (AC-7)', async ({ page, request }) => {
    const user = await createUser(request);
    await loginAs(page, user);
    await page.goto('/transactions/0193a0b2-0000-7000-8000-00000000dead');
    await expect(page.getByText('Transaction not found.')).toBeVisible();
  });

  test('no horizontal scroll at phone width (AC-11) @mobile', async ({ page, request }) => {
    const user = await createUser(request);
    await loginAs(page, user);
    await page.setViewportSize({ width: 360, height: 740 });
    for (const path of ['/', '/history']) {
      await page.goto(path);
      await expect(page.getByRole('main')).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    }
  });
});
