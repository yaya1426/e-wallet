// Test doubles for the session and API client.
import { vi } from 'vitest';
import type { SessionDeps } from '@/auth/session';

export const ORIGIN = 'http://localhost:3000';

export type Handler = (req: Request, body: unknown) => Response | Promise<Response>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });
}

export function apiError(status: number, code: string, requestId = 'req-test-1'): Response {
  return json(status, { error: { code, message: 'server text that must not be shown', request_id: requestId } });
}

export function tokens(n: number, expiresIn = 900) {
  return {
    access_token: `access-${n}`,
    token_type: 'Bearer',
    expires_in: expiresIn,
    refresh_token: `refresh-${n}`,
    refresh_expires_in: 2_592_000,
  };
}

/** A fetch that routes "METHOD /path" to handlers and records every call. */
export class FakeServer {
  readonly calls: { method: string; path: string; body: unknown; auth: string | null }[] = [];
  private readonly handlers = new Map<string, Handler>();

  on(route: string, handler: Handler): this {
    this.handlers.set(route, handler);
    return this;
  }

  readonly fetch = vi.fn(async (req: Request): Promise<Response> => {
    const url = new URL(req.url);
    const text = await req.text();
    const body: unknown = text ? JSON.parse(text) : undefined;
    const route = `${req.method} ${url.pathname}`;
    this.calls.push({ method: req.method, path: url.pathname, body, auth: req.headers.get('Authorization') });
    const handler = this.handlers.get(route);
    if (!handler) throw new Error(`FakeServer: no handler for ${route}`);
    return handler(req, body);
  });

  count(route: string): number {
    return this.calls.filter((c) => `${c.method} ${c.path}` === route).length;
  }
}

export class MemoryStorage {
  readonly map = new Map<string, string>();
  getItem(key: string): string | null {
    return this.map.get(key) ?? null;
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value);
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
}

/** An exclusive lock like navigator.locks, shared by several Session instances ("tabs"). */
export class FakeLocks {
  private tail: Promise<unknown> = Promise.resolve();
  held = 0;
  maxHeld = 0;
  request<T>(_name: string, _opts: { mode: 'exclusive' }, cb: () => Promise<T>): Promise<T> {
    const run = this.tail.then(async () => {
      this.held++;
      this.maxHeld = Math.max(this.maxHeld, this.held);
      try {
        return await cb();
      } finally {
        this.held--;
      }
    });
    this.tail = run.catch(() => undefined);
    return run;
  }
}

export interface FakeEnv {
  server: FakeServer;
  storage: MemoryStorage;
  locks: FakeLocks;
  clock: { now: number };
  timers: { fn: () => void; at: number; id: number }[];
  storageListeners: ((key: string | null, value: string | null) => void)[];
  deps: () => SessionDeps;
  /** Runs timers that are due at the current fake time. */
  runDueTimers: () => void;
}

export function fakeEnv(server = new FakeServer(), shared?: Pick<FakeEnv, 'storage' | 'locks' | 'clock'>): FakeEnv {
  const env: FakeEnv = {
    server,
    storage: shared?.storage ?? new MemoryStorage(),
    locks: shared?.locks ?? new FakeLocks(),
    clock: shared?.clock ?? { now: 1_800_000_000_000 },
    timers: [],
    storageListeners: [],
    deps: () => ({
      fetch: server.fetch,
      storage: env.storage,
      locks: env.locks,
      now: () => env.clock.now,
      setTimer: (fn, ms) => {
        const id = Math.random();
        env.timers.push({ fn, at: env.clock.now + ms, id });
        return id;
      },
      clearTimer: (id) => {
        env.timers = env.timers.filter((t) => t.id !== id);
      },
      onStorageChange: (l) => {
        env.storageListeners.push(l);
        return () => {
          env.storageListeners = env.storageListeners.filter((x) => x !== l);
        };
      },
      origin: ORIGIN,
    }),
    runDueTimers: () => {
      const due = env.timers.filter((t) => t.at <= env.clock.now);
      env.timers = env.timers.filter((t) => t.at > env.clock.now);
      due.forEach((t) => t.fn());
    },
  };
  return env;
}

export const flush = () => new Promise((r) => setTimeout(r, 0));
