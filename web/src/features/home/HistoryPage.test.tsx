import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { apiError, FakeServer, json } from '@/test/fakes';
import { page, tx, wallet } from '@/test/fixtures';
import { renderApp } from '@/test/renderApp';

const all = Array.from({ length: 45 }, (_, i) => tx({ amount_minor: (i + 1) * 100 }));

function pagedServer(failCursor?: string) {
  let failures = 0;
  return new FakeServer().on('GET /v1/me/wallet', () => json(200, wallet())).on('GET /v1/me/transactions', (req) => {
    const url = new URL(req.url);
    const limit = Number(url.searchParams.get('limit'));
    const cursor = url.searchParams.get('cursor');
    // Fails twice: the first try and the query's single automatic retry.
    if (cursor && cursor === failCursor && failures < 2) {
      failures++;
      return apiError(500, 'INTERNAL_ERROR', 'req-9');
    }
    const start = cursor ? Number(cursor) : 0;
    const items = all.slice(start, start + limit);
    const next = start + limit < all.length ? String(start + limit) : null;
    return json(200, page(items, next));
  });
}

const rows = () => within(screen.getByRole('main')).getAllByRole('listitem');

describe('HistoryPage (AC-5)', () => {
  it('loads 20, then 40, then 45 and hides "Load more"', async () => {
    await renderApp('/history', { server: pagedServer(), signedIn: true });
    await screen.findAllByText('Top-up');
    expect(rows()).toHaveLength(20);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Load more' }));
    await screen.findByText('+EGP 40.00');
    expect(rows()).toHaveLength(40);
    await user.click(screen.getByRole('button', { name: 'Load more' }));
    await screen.findByText('+EGP 45.00');
    expect(rows()).toHaveLength(45);
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });

  it('a failed page keeps the loaded rows and retries that page', async () => {
    await renderApp('/history', { server: pagedServer('20'), signedIn: true });
    await screen.findAllByText('Top-up');
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Reference: req-9');
    expect(rows()).toHaveLength(20);
    await user.click(screen.getByRole('button', { name: 'Try again' }));
    await screen.findByText('+EGP 40.00');
    expect(rows()).toHaveLength(40);
  });

  it('empty history (AC-3)', async () => {
    const s = new FakeServer()
      .on('GET /v1/me/wallet', () => json(200, wallet()))
      .on('GET /v1/me/transactions', () => json(200, page([])));
    await renderApp('/history', { server: s, signedIn: true });
    expect(await screen.findByText('No transactions yet.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument();
  });

  it('INVALID_CURSOR restarts from the first page', async () => {
    let calls = 0;
    const s = new FakeServer().on('GET /v1/me/wallet', () => json(200, wallet())).on('GET /v1/me/transactions', (req) => {
      calls++;
      const cursor = new URL(req.url).searchParams.get('cursor');
      if (cursor) return apiError(400, 'INVALID_CURSOR');
      return json(200, page(all.slice(0, 20), 'stale'));
    });
    await renderApp('/history', { server: s, signedIn: true });
    await screen.findAllByText('Top-up');
    await userEvent.setup().click(screen.getByRole('button', { name: 'Load more' }));
    await screen.findByRole('button', { name: 'Load more' }, { timeout: 3000 });
    expect(rows()).toHaveLength(20);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(calls).toBe(3); // first page, bad cursor, first page again
  });
});
