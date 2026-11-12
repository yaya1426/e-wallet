import { describe, expect, it } from 'vitest';
import { ApiError } from '@/api/errors';
import { apiError, fakeEnv, FakeServer, flush, json, tokens } from '@/test/fakes';
import { REFRESH_AHEAD_MS, Session, STORAGE_KEY } from './session';

function stored(env: ReturnType<typeof fakeEnv>) {
  const raw = env.storage.getItem(STORAGE_KEY);
  return raw ? (JSON.parse(raw) as { refresh_token: string; refresh_expires_at: number }) : null;
}

function seed(env: ReturnType<typeof fakeEnv>, token = 'refresh-0') {
  env.storage.setItem(
    STORAGE_KEY,
    JSON.stringify({ refresh_token: token, refresh_expires_at: env.clock.now + 86_400_000 }),
  );
}

/** A refresh endpoint that rotates tokens and rejects reuse like TDR-018 §3. */
function rotatingServer() {
  const server = new FakeServer();
  let n = 0;
  const used = new Set<string>();
  server.on('POST /v1/auth/refresh', (_req, body) => {
    const token = (body as { refresh_token: string }).refresh_token;
    if (used.has(token)) return apiError(401, 'INVALID_REFRESH_TOKEN');
    used.add(token);
    n++;
    return json(200, tokens(n));
  });
  return { server, used };
}

describe('Session', () => {
  it('login stores the refresh token and keeps the access token in memory', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');

    expect(s.getState()).toEqual({ status: 'authenticated', endReason: null });
    expect(s.getAccessToken()).toBe('access-1');
    expect(stored(env)).toEqual({ refresh_token: 'refresh-1', refresh_expires_at: env.clock.now + 2_592_000_000 });
    expect(JSON.stringify([...env.storage.map.values()])).not.toContain('access-1');
  });

  it('login failure throws the API error and leaves the state alone', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => apiError(401, 'INVALID_CREDENTIALS'));
    const s = new Session(env.deps());
    await s.restore();
    await expect(s.login('+201001234567', 'wrong')).rejects.toMatchObject({ code: 'INVALID_CREDENTIALS', status: 401 });
    expect(s.getState().status).toBe('anonymous');
    expect(stored(env)).toBeNull();
  });

  it('login network failure is a NETWORK_ERROR', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => {
      throw new TypeError('Failed to fetch');
    });
    const s = new Session(env.deps());
    await expect(s.login('+201001234567', 'x')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
  });

  describe('restore (AC-9)', () => {
    it('without a stored token → anonymous, no request', async () => {
      const env = fakeEnv();
      const s = new Session(env.deps());
      await s.restore();
      expect(s.getState()).toEqual({ status: 'anonymous', endReason: null });
      expect(env.server.calls).toHaveLength(0);
    });

    it('with an expired or corrupt stored token → anonymous, storage cleared', async () => {
      for (const raw of ['{bad json', JSON.stringify({ refresh_token: 'r', refresh_expires_at: 1 }), '{}']) {
        const env = fakeEnv();
        env.storage.setItem(STORAGE_KEY, raw);
        const s = new Session(env.deps());
        await s.restore();
        expect(s.getState().status).toBe('anonymous');
        expect(env.storage.getItem(STORAGE_KEY)).toBeNull();
        expect(env.server.calls).toHaveLength(0);
      }
    });

    it('with a valid stored token → refreshes and is authenticated', async () => {
      const { server } = rotatingServer();
      const env = fakeEnv(server);
      seed(env);
      const s = new Session(env.deps());
      await s.restore();
      expect(s.getState().status).toBe('authenticated');
      expect(s.getAccessToken()).toBe('access-1');
      expect(stored(env)?.refresh_token).toBe('refresh-1');
    });

    it('refresh rejected (401) → session ended, storage cleared (AC-12)', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/refresh', () => apiError(401, 'INVALID_REFRESH_TOKEN'));
      seed(env);
      const s = new Session(env.deps());
      await s.restore();
      expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'sessionEnded' });
      expect(stored(env)).toBeNull();
    });

    it('disabled user (403) → userDisabled, storage cleared (AC-13)', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/refresh', () => apiError(403, 'USER_DISABLED'));
      seed(env);
      const s = new Session(env.deps());
      await s.restore();
      expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'userDisabled' });
      expect(stored(env)).toBeNull();
    });

    it('network error or 5xx → anonymous with "network", storage kept for a retry', async () => {
      for (const respond of [
        () => {
          throw new TypeError('offline');
        },
        () => apiError(500, 'INTERNAL_ERROR'),
      ]) {
        const env = fakeEnv();
        env.server.on('POST /v1/auth/refresh', respond);
        seed(env);
        const s = new Session(env.deps());
        await s.restore();
        expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'network' });
        expect(stored(env)?.refresh_token).toBe('refresh-0');
      }
    });
  });

  it('parallel refresh calls in one tab share one request (AC-10)', async () => {
    const { server } = rotatingServer();
    const env = fakeEnv(server);
    seed(env);
    const s = new Session(env.deps());
    const results = await Promise.all([s.refresh(), s.refresh(), s.refresh()]);
    expect(results).toEqual(['access-1', 'access-1', 'access-1']);
    expect(server.count('POST /v1/auth/refresh')).toBe(1);
  });

  it('two tabs refreshing at once are serialized and never reuse a token (AC-11)', async () => {
    const { server } = rotatingServer();
    const tabA = fakeEnv(server);
    const tabB = fakeEnv(server, tabA);
    seed(tabA);
    const a = new Session(tabA.deps());
    const b = new Session(tabB.deps());

    const [ta, tb] = await Promise.all([a.refresh(), b.refresh()]);

    expect(tabA.locks.maxHeld).toBe(1);
    const sent = server.calls.map((c) => (c.body as { refresh_token: string }).refresh_token);
    expect(sent).toEqual(['refresh-0', 'refresh-1']); // B used what A wrote
    expect(new Set([ta, tb]).size).toBe(2);
    expect(a.getState().status).toBe('authenticated');
    expect(b.getState().status).toBe('authenticated');
    expect(stored(tabA)?.refresh_token).toBe('refresh-2');
  });

  it('refreshes proactively 60 s before the access token expires', async () => {
    const { server } = rotatingServer();
    const env = fakeEnv(server);
    env.server.on('POST /v1/auth/login', () => json(200, tokens(100)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    expect(env.timers).toHaveLength(1);
    expect(env.timers[0]?.at).toBe(env.clock.now + 900_000 - REFRESH_AHEAD_MS);

    env.clock.now += 900_000 - REFRESH_AHEAD_MS;
    env.runDueTimers();
    await flush();
    expect(server.count('POST /v1/auth/refresh')).toBe(1);
    expect(s.getAccessToken()).toBe('access-1');
    expect(env.timers).toHaveLength(1); // next refresh scheduled
  });

  it('a proactive refresh that hits a network error retries while the token is still valid', async () => {
    const env = fakeEnv();
    let fail = true;
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    env.server.on('POST /v1/auth/refresh', () => {
      if (fail) throw new TypeError('offline');
      return json(200, tokens(2));
    });
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    env.clock.now += 840_000;
    env.runDueTimers();
    await flush();
    expect(s.getState().status).toBe('authenticated');
    expect(env.timers).toHaveLength(1);
    fail = false;
    env.clock.now += 30_000;
    env.runDueTimers();
    await flush();
    expect(s.getAccessToken()).toBe('access-2');
  });

  it('getAccessToken returns null once the access token has expired', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    env.clock.now += 900_000;
    expect(s.getAccessToken()).toBeNull();
  });

  it('refresh without a stored token ends the session', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    env.storage.removeItem(STORAGE_KEY);
    await expect(s.refresh()).rejects.toBeInstanceOf(ApiError);
    expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'sessionEnded' });
    expect(env.server.count('POST /v1/auth/refresh')).toBe(0);
  });

  describe('logout (AC-14)', () => {
    it('calls the API with both tokens, then clears everything', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
      env.server.on('POST /v1/auth/logout', () => new Response(null, { status: 204 }));
      const s = new Session(env.deps());
      await s.login('+201001234567', 'secret123');
      await s.logout();
      const call = env.server.calls.find((c) => c.path === '/v1/auth/logout');
      expect(call).toMatchObject({ auth: 'Bearer access-1', body: { refresh_token: 'refresh-1' } });
      expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'signedOut' });
      expect(stored(env)).toBeNull();
      expect(s.getAccessToken()).toBeNull();
      expect(env.timers).toHaveLength(0);
    });

    it('still signs out locally when the API call fails', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
      env.server.on('POST /v1/auth/logout', () => {
        throw new TypeError('offline');
      });
      const s = new Session(env.deps());
      await s.login('+201001234567', 'secret123');
      await s.logout();
      expect(s.getState().status).toBe('anonymous');
      expect(stored(env)).toBeNull();
    });

    it('refreshes first when the access token has expired, so the server session is revoked', async () => {
      const { server } = rotatingServer();
      const env = fakeEnv(server);
      server.on('POST /v1/auth/login', () => json(200, tokens(100)));
      server.on('POST /v1/auth/logout', () => new Response(null, { status: 204 }));
      const s = new Session(env.deps());
      await s.login('+201001234567', 'secret123');
      env.clock.now += 1_000_000;
      await s.logout();
      const call = server.calls.find((c) => c.path === '/v1/auth/logout');
      expect(call).toMatchObject({ auth: 'Bearer access-1', body: { refresh_token: 'refresh-1' } });
    });

    it('another tab follows when the stored session is removed', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
      const s = new Session(env.deps());
      await s.login('+201001234567', 'secret123');
      env.storageListeners.forEach((l) => l(STORAGE_KEY, null));
      expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'signedOut' });
      expect(s.getAccessToken()).toBeNull();
    });

    it('another tab rotating the token does not sign this one out', async () => {
      const env = fakeEnv();
      env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
      const s = new Session(env.deps());
      await s.login('+201001234567', 'secret123');
      env.storageListeners.forEach((l) => l(STORAGE_KEY, '{"refresh_token":"x","refresh_expires_at":9e15}'));
      expect(s.getState().status).toBe('authenticated');
    });
  });

  it('a refresh that finishes after the session ended does not bring it back', async () => {
    const env = fakeEnv();
    let release: (r: Response) => void = () => undefined;
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    env.server.on('POST /v1/auth/refresh', () => new Promise<Response>((r) => (release = r)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    const pending = s.refresh();
    await flush();
    s.end('userDisabled');
    release(json(200, tokens(2)));
    await expect(pending).rejects.toBeInstanceOf(ApiError);
    expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'userDisabled' });
    expect(stored(env)).toBeNull();
    expect(s.getAccessToken()).toBeNull();
  });

  it.each([400, 403, 404, 413])('refresh answered %d is terminal (no stuck "network" state)', async (status) => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/refresh', () => apiError(status, 'MALFORMED_REQUEST'));
    seed(env);
    const s = new Session(env.deps());
    await s.restore();
    expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'sessionEnded' });
    expect(stored(env)).toBeNull();
  });

  it('429 on refresh keeps the session for a later retry', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/refresh', () => apiError(429, 'TOO_MANY_REQUESTS'));
    seed(env);
    const s = new Session(env.deps());
    await s.restore();
    expect(s.getState()).toEqual({ status: 'anonymous', endReason: 'network' });
    expect(stored(env)).not.toBeNull();
  });

  it('never schedules refreshes closer than 5 s, even with a tiny JWT_TTL', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1, 30)));
    const s = new Session(env.deps());
    await s.login('+201001234567', 'secret123');
    expect(env.timers[0]?.at).toBe(env.clock.now + 5_000);
  });

  it('session requests carry a timeout signal', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', (req) => {
      expect(req.signal).toBeDefined();
      return json(200, tokens(1));
    });
    await new Session(env.deps()).login('+201001234567', 'secret123');
  });

  it('notifies subscribers on every state change', async () => {
    const env = fakeEnv();
    env.server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    const s = new Session(env.deps());
    const seen: string[] = [];
    s.subscribe((st) => seen.push(`${st.status}/${st.endReason ?? '-'}`));
    await s.restore();
    await s.login('+201001234567', 'secret123');
    s.end('userDisabled');
    expect(seen).toEqual(['anonymous/-', 'authenticated/-', 'anonymous/userDisabled']);
  });

  it('works without navigator.locks (single-flight in the tab only)', async () => {
    const { server } = rotatingServer();
    const env = fakeEnv(server);
    seed(env);
    const s = new Session({ ...env.deps(), locks: undefined });
    await Promise.all([s.refresh(), s.refresh()]);
    expect(server.count('POST /v1/auth/refresh')).toBe(1);
  });
});
