import { describe, expect, it } from 'vitest';
import { browserOpsKeyDeps, OPS_STORAGE_KEY, OpsKeyStore } from '@/features/ops/opsKey';
import { apiError, FakeServer, json, ORIGIN, fakeEnv } from '@/test/fakes';
import { Session } from '@/auth/session';
import { createApiClient } from './client';
import { createOpsClient } from './opsClient';

const health = { status: 'ok', discrepancies: {}, stuck: {}, outbox: {}, inbox: {}, jobs: [] };

describe('opsClient (T034 AC-2, TDR-022 §4)', () => {
  it('sends X-Internal-Api-Key and never Authorization', async () => {
    const s = new FakeServer().on('GET /internal/v1/health/ops', () => json(200, health));
    const store = new OpsKeyStore(browserOpsKeyDeps());
    store.set('ops-key');
    const ops = createOpsClient(store, s.fetch, ORIGIN);
    await ops.GET('/internal/v1/health/ops', { headers: { Authorization: 'Bearer leaked' } });
    const req = s.fetch.mock.calls[0]?.[0] as Request;
    expect(req.headers.get('X-Internal-Api-Key')).toBe('ops-key');
    expect(req.headers.get('Authorization')).toBeNull();
  });

  it('a GET (auto-refresh) leaves the idle window alone; a POST (operator action) restarts it (AC-1)', async () => {
    let now = 1_000_000;
    const store = new OpsKeyStore({ ...browserOpsKeyDeps(), now: () => now });
    const s = new FakeServer()
      .on('GET /internal/v1/health/ops', () => json(200, health))
      .on('POST /internal/v1/wallets/0193a0b2-0000-7000-8000-000000000001/freeze', () => json(200, { wallet_id: 'x', status: 'FROZEN' }));
    store.set('k');
    const ops = createOpsClient(store, s.fetch, ORIGIN);
    const lastActive = () => JSON.parse(window.sessionStorage.getItem(OPS_STORAGE_KEY) ?? '{}').lastActive as number;
    now += 60_000;
    await ops.GET('/internal/v1/health/ops');
    expect(lastActive()).toBe(1_000_000);
    await ops.POST('/internal/v1/wallets/{wallet_id}/freeze', { params: { path: { wallet_id: '0193a0b2-0000-7000-8000-000000000001' } } });
    expect(lastActive()).toBe(1_060_000);
  });

  it('a 401 drops the key with reason "rejected"', async () => {
    const s = new FakeServer().on('GET /internal/v1/health/ops', () => apiError(401, 'UNAUTHENTICATED'));
    const store = new OpsKeyStore(browserOpsKeyDeps());
    store.set('old-key');
    await createOpsClient(store, s.fetch, ORIGIN).GET('/internal/v1/health/ops');
    expect(store.get()).toBeNull();
    expect(store.getSnapshot()).toEqual({ unlocked: false, reason: 'rejected' });
  });

  it('the user client never sends the operator key, even when one is stored', async () => {
    const s = new FakeServer()
      .on('POST /v1/auth/login', () => json(200, { access_token: 'a', token_type: 'Bearer', expires_in: 900, refresh_token: 'r', refresh_expires_in: 1 }))
      .on('GET /v1/me/wallet', () => json(200, {}));
    const store = new OpsKeyStore(browserOpsKeyDeps());
    store.set('ops-key');
    const session = new Session(fakeEnv(s).deps());
    await session.login('+201001234567', 'secret123');
    await createApiClient(session, s.fetch, ORIGIN).GET('/v1/me/wallet');
    for (const [r] of s.fetch.mock.calls) expect((r as Request).headers.get('X-Internal-Api-Key')).toBeNull();
  });
});
