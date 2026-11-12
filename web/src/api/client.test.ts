import { describe, expect, it } from 'vitest';
import { Session } from '@/auth/session';
import { apiError, fakeEnv, FakeServer, json, ORIGIN, tokens } from '@/test/fakes';
import { createApiClient, unwrap } from './client';
import { ApiError } from './errors';

const wallet = { wallet_id: 'w-1', currency: 'EGP', available_balance_minor: 15075, status: 'ACTIVE' };

async function signedIn(server: FakeServer) {
  const env = fakeEnv(server);
  let n = 0;
  server.on('POST /v1/auth/login', () => json(200, tokens(++n)));
  server.on('POST /v1/auth/refresh', () => json(200, tokens(++n)));
  const session = new Session(env.deps());
  await session.login('+201001234567', 'secret123');
  const api = createApiClient(session, server.fetch, ORIGIN);
  return { env, session, api };
}

describe('API client', () => {
  it('sends the access token on authenticated paths only', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => json(200, wallet));
    server.on('POST /v1/auth/register', () => json(201, {}));
    const { api } = await signedIn(server);

    expect(unwrap(await api.GET('/v1/me/wallet'))).toEqual(wallet);
    await api.POST('/v1/auth/register', { body: { phone: '+201001234567', full_name: 'Al', password: 'x' } });

    expect(server.calls.find((c) => c.path === '/v1/me/wallet')?.auth).toBe('Bearer access-1');
    expect(server.calls.find((c) => c.path === '/v1/auth/register')?.auth).toBeNull();
  });

  it('three parallel calls that get 401 share one refresh and are retried once (AC-10)', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', (req) =>
      req.headers.get('Authorization') === 'Bearer access-2' ? json(200, wallet) : apiError(401, 'UNAUTHENTICATED'),
    );
    const { api } = await signedIn(server);

    const results = await Promise.all([api.GET('/v1/me/wallet'), api.GET('/v1/me/wallet'), api.GET('/v1/me/wallet')]);

    expect(results.map((r) => r.response.status)).toEqual([200, 200, 200]);
    expect(server.count('POST /v1/auth/refresh')).toBe(1);
    expect(server.count('GET /v1/me/wallet')).toBe(6);
  });

  it('retries a POST with its original body after refreshing', async () => {
    const server = new FakeServer();
    const bodies: unknown[] = [];
    server.on('PUT /v1/me/bank-account', (req, body) => {
      bodies.push(body);
      return req.headers.get('Authorization') === 'Bearer access-2'
        ? json(200, { ok: true })
        : apiError(401, 'UNAUTHENTICATED');
    });
    const { api } = await signedIn(server);
    const res = await api.PUT('/v1/me/bank-account', { body: { iban: 'EG00', holder_name: 'Al' } });
    expect(res.response.status).toBe(200);
    expect(bodies).toEqual([
      { iban: 'EG00', holder_name: 'Al' },
      { iban: 'EG00', holder_name: 'Al' },
    ]);
  });

  it('a second 401 after a refresh ends the session', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => apiError(401, 'UNAUTHENTICATED'));
    const { api, session } = await signedIn(server);
    const res = await api.GET('/v1/me/wallet');
    expect(res.response.status).toBe(401);
    expect(session.getState()).toEqual({ status: 'anonymous', endReason: 'sessionEnded' });
  });

  it('a 401 whose refresh is rejected ends the session without a retry (AC-12)', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => apiError(401, 'UNAUTHENTICATED'));
    const { api, session } = await signedIn(server);
    server.on('POST /v1/auth/refresh', () => apiError(401, 'INVALID_REFRESH_TOKEN'));
    await api.GET('/v1/me/wallet');
    expect(server.count('GET /v1/me/wallet')).toBe(1);
    expect(session.getState()).toEqual({ status: 'anonymous', endReason: 'sessionEnded' });
  });

  it('403 USER_DISABLED from any endpoint ends the session (AC-13)', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => apiError(403, 'USER_DISABLED'));
    const { api, session, env } = await signedIn(server);
    const result = await api.GET('/v1/me/wallet');
    expect(() => unwrap(result)).toThrow(ApiError);
    expect(session.getState()).toEqual({ status: 'anonymous', endReason: 'userDisabled' });
    expect(env.storage.map.size).toBe(0);
  });

  it('other 401 codes (e.g. wrong credentials) do not trigger a refresh', async () => {
    const server = new FakeServer();
    server.on('POST /v1/auth/login', () => apiError(401, 'INVALID_CREDENTIALS'));
    const env = fakeEnv(server);
    const session = new Session(env.deps());
    const api = createApiClient(session, server.fetch, ORIGIN);
    const res = await api.POST('/v1/auth/login', { body: { phone: '+201001234567', password: 'x' } });
    expect(res.response.status).toBe(401);
    expect(server.count('POST /v1/auth/refresh')).toBe(0);
  });

  it('an anonymous caller gets its 401 back without a refresh or a session-ended notice', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => apiError(401, 'UNAUTHENTICATED'));
    const env = fakeEnv(server);
    const session = new Session(env.deps());
    await session.restore();
    const api = createApiClient(session, server.fetch, ORIGIN);
    const res = await api.GET('/v1/me/wallet');
    expect(res.response.status).toBe(401);
    expect(server.count('POST /v1/auth/refresh')).toBe(0);
    expect(session.getState()).toEqual({ status: 'anonymous', endReason: null });
  });

  it('a late 401 for an old token retries with the newer token without another refresh', async () => {
    const server = new FakeServer();
    const { api, session } = await signedIn(server);
    let releaseSlow: (r: Response) => void = () => undefined;
    server.on('GET /v1/me/wallet', (req) => {
      const auth = req.headers.get('Authorization');
      if (auth === 'Bearer access-1') return new Promise<Response>((r) => (releaseSlow = r));
      return json(200, wallet);
    });
    const slow = api.GET('/v1/me/wallet');
    await new Promise((r) => setTimeout(r, 0));
    await session.refresh(); // someone else refreshed meanwhile → access-2
    releaseSlow(apiError(401, 'UNAUTHENTICATED'));
    const res = await slow;
    expect(res.response.status).toBe(200);
    expect(server.count('POST /v1/auth/refresh')).toBe(1);
  });

  it('when the token expired and the refresh fails on the network, the call fails with NETWORK_ERROR', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => json(200, wallet));
    const { api, session, env } = await signedIn(server);
    server.on('POST /v1/auth/refresh', () => {
      throw new TypeError('offline');
    });
    env.clock.now += 900_000; // access token expired
    await expect(api.GET('/v1/me/wallet')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    expect(server.count('GET /v1/me/wallet')).toBe(0);
    expect(session.getState().status).toBe('authenticated');
  });

  it('unwrap turns error bodies into ApiError with code and request id', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => apiError(500, 'INTERNAL_ERROR', 'req-42'));
    const { api } = await signedIn(server);
    await expect(api.GET('/v1/me/wallet').then(unwrap)).rejects.toMatchObject({
      status: 500,
      code: 'INTERNAL_ERROR',
      requestId: 'req-42',
    });
  });

  it('unwrap reports a non-JSON error as UNKNOWN with the X-Request-ID header', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => new Response('bad gateway', { status: 502, headers: { 'X-Request-ID': 'req-7' } }));
    const { api } = await signedIn(server);
    await expect(api.GET('/v1/me/wallet').then(unwrap)).rejects.toMatchObject({
      status: 502,
      code: 'UNKNOWN',
      requestId: 'req-7',
    });
  });

  it('a network failure becomes NETWORK_ERROR', async () => {
    const server = new FakeServer();
    server.on('GET /v1/me/wallet', () => {
      throw new TypeError('Failed to fetch');
    });
    const { api } = await signedIn(server);
    await expect(api.GET('/v1/me/wallet')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
  });
});
