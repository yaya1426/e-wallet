// Browser session handling (TDR-020). Framework-free so it can be unit tested with fakes:
// - the access token lives in memory only;
// - the rotating refresh token lives in storage under STORAGE_KEY;
// - every refresh runs under a cross-tab lock and re-reads storage inside it, so two tabs
//   never send the same refresh token (TDR-018 §3 reuse detection);
// - a proactive timer refreshes 60 s before the access token expires.
import { ApiError, apiErrorFrom, networkError } from '@/api/errors';
import type { components } from '@/api/schema';

type LoginResponse = components['schemas']['LoginResponse'];

export const STORAGE_KEY = 'ewallet.session.v1';
export const LOCK_NAME = 'ewallet-refresh';
export const REFRESH_AHEAD_MS = 60_000;
export const NETWORK_RETRY_MS = 30_000;
/** Never refresh more often than this, whatever JWT_TTL is. */
export const MIN_REFRESH_DELAY_MS = 5_000;
/** A session request that takes longer is treated as a network failure (it holds the lock). */
export const REQUEST_TIMEOUT_MS = 15_000;

export type SessionStatus = 'loading' | 'authenticated' | 'anonymous';
/** Why the user is anonymous; each maps to a notice on the login page (PRD-005 UI-BR-6/11/12). */
export type EndReason = 'sessionEnded' | 'userDisabled' | 'network' | 'signedOut';

export interface SessionState {
  status: SessionStatus;
  endReason: EndReason | null;
}

interface StoredSession {
  refresh_token: string;
  refresh_expires_at: number;
}

interface LockLike {
  request<T>(name: string, options: { mode: 'exclusive' }, callback: () => Promise<T>): Promise<T>;
}

export interface SessionDeps {
  fetch: (input: Request) => Promise<Response>;
  storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
  /** navigator.locks; when missing only same-tab single-flight is used. */
  locks?: LockLike;
  now: () => number;
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
  /** Subscribes to storage changes made by other tabs; returns an unsubscribe function. */
  onStorageChange?: (listener: (key: string | null, newValue: string | null) => void) => () => void;
  origin: string;
}

const terminalRefreshCodes = new Set(['INVALID_REFRESH_TOKEN', 'USER_DISABLED']);

export class Session {
  private readonly deps: SessionDeps;
  private access: { token: string; expiresAt: number } | null = null;
  private state: SessionState = { status: 'loading', endReason: null };
  private readonly listeners = new Set<(s: SessionState) => void>();
  private inflight: Promise<string> | null = null;
  /** Bumped whenever the session ends, so a refresh that was already running cannot revive it. */
  private generation = 0;
  private timer: unknown = null;
  private readonly unsubscribeStorage: (() => void) | undefined;

  constructor(deps: SessionDeps) {
    this.deps = deps;
    this.unsubscribeStorage = deps.onStorageChange?.((key, newValue) => {
      // Another tab signed out (or its session ended): follow it (TDR-020 §3).
      if ((key === STORAGE_KEY || key === null) && newValue === null && this.state.status === 'authenticated') {
        this.endLocally('signedOut');
      }
    });
  }

  getState(): SessionState {
    return this.state;
  }

  subscribe(listener: (s: SessionState) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** The in-memory access token if it has not expired yet. */
  getAccessToken(): string | null {
    if (!this.access || this.access.expiresAt <= this.deps.now()) return null;
    return this.access.token;
  }

  /** On app start: turn a stored refresh token into a session (TDR-020 §2a). */
  async restore(): Promise<void> {
    this.setState({ status: 'loading', endReason: null });
    const stored = this.readStored();
    if (!stored) {
      this.deps.storage.removeItem(STORAGE_KEY);
      this.setState({ status: 'anonymous', endReason: null });
      return;
    }
    try {
      await this.refresh();
    } catch (err) {
      if (this.state.status === 'loading') {
        // Not terminal (network / 5xx): keep the stored token so a retry can still work.
        this.setState({ status: 'anonymous', endReason: isTerminal(err) ? 'sessionEnded' : 'network' });
      }
    }
  }

  /** POST /v1/auth/login. Throws ApiError on failure without touching the current state. */
  async login(phone: string, password: string): Promise<void> {
    const res = await this.post('/v1/auth/login', { phone, password });
    if (!res.ok) throw await errorOf(res);
    this.generation++; // a refresh of an older session still in flight must not overwrite this one
    this.applyTokens((await res.json()) as LoginResponse);
  }

  /**
   * Gets a new access token. Callers in the same tab share one request; tabs are serialized by
   * the lock. Terminal failures end the session before the error is thrown.
   */
  refresh(): Promise<string> {
    if (!this.inflight) {
      this.inflight = this.withLock(() => this.refreshUnderLock()).finally(() => {
        this.inflight = null;
      });
    }
    return this.inflight;
  }

  /** Signs out on the server (best effort) and in every tab (TDR-020 §3). */
  async logout(): Promise<void> {
    if (!this.getAccessToken()) {
      await this.refresh().catch(() => undefined);
    }
    const token = this.getAccessToken();
    await this.withLock(async () => {
      const stored = this.readStored();
      if (stored && token) {
        await this.post('/v1/auth/logout', { refresh_token: stored.refresh_token }, token).catch(() => undefined);
      }
      this.deps.storage.removeItem(STORAGE_KEY);
    });
    this.endLocally('signedOut');
  }

  /** Ends the session here and in other tabs (clears storage). */
  end(reason: EndReason): void {
    this.deps.storage.removeItem(STORAGE_KEY);
    this.endLocally(reason);
  }

  dispose(): void {
    this.cancelTimer();
    this.unsubscribeStorage?.();
    this.listeners.clear();
  }

  private async refreshUnderLock(): Promise<string> {
    const generation = this.generation;
    const stored = this.readStored();
    if (!stored) {
      this.end('sessionEnded');
      throw new ApiError(401, 'INVALID_REFRESH_TOKEN');
    }
    const res = await this.post('/v1/auth/refresh', { refresh_token: stored.refresh_token });
    if (res.ok) {
      const body = (await res.json()) as LoginResponse;
      if (generation !== this.generation) {
        // The session ended while this refresh was running: keep it ended.
        throw new ApiError(401, 'INVALID_REFRESH_TOKEN');
      }
      this.applyTokens(body);
      return body.access_token;
    }
    const err = await errorOf(res);
    if (err.code === 'USER_DISABLED') {
      this.end('userDisabled');
    } else if (isTerminal(err)) {
      // 401 and any other 4xx except 429: retrying the same token can never work.
      this.end('sessionEnded');
    }
    throw err;
  }

  private applyTokens(body: LoginResponse): void {
    const now = this.deps.now();
    const stored: StoredSession = {
      refresh_token: body.refresh_token,
      refresh_expires_at: now + body.refresh_expires_in * 1000,
    };
    this.deps.storage.setItem(STORAGE_KEY, JSON.stringify(stored));
    this.access = { token: body.access_token, expiresAt: now + body.expires_in * 1000 };
    this.schedule(Math.max(MIN_REFRESH_DELAY_MS, body.expires_in * 1000 - REFRESH_AHEAD_MS));
    this.setState({ status: 'authenticated', endReason: null });
  }

  private schedule(ms: number): void {
    this.cancelTimer();
    this.timer = this.deps.setTimer(() => {
      this.timer = null;
      this.refresh().catch((err: unknown) => {
        // Network trouble: try again while the current access token is still valid.
        if (!isTerminal(err) && this.state.status === 'authenticated' && this.getAccessToken()) {
          this.schedule(NETWORK_RETRY_MS);
        }
      });
    }, ms);
  }

  private cancelTimer(): void {
    if (this.timer !== null) {
      this.deps.clearTimer(this.timer);
      this.timer = null;
    }
  }

  private endLocally(reason: EndReason): void {
    this.generation++;
    this.cancelTimer();
    this.access = null;
    this.setState({ status: 'anonymous', endReason: reason });
  }

  private readStored(): StoredSession | null {
    const raw = this.deps.storage.getItem(STORAGE_KEY);
    if (!raw) return null;
    try {
      const parsed = JSON.parse(raw) as Partial<StoredSession>;
      if (
        typeof parsed.refresh_token !== 'string' ||
        parsed.refresh_token === '' ||
        typeof parsed.refresh_expires_at !== 'number' ||
        parsed.refresh_expires_at <= this.deps.now()
      ) {
        return null;
      }
      return { refresh_token: parsed.refresh_token, refresh_expires_at: parsed.refresh_expires_at };
    } catch {
      return null;
    }
  }

  private withLock<T>(fn: () => Promise<T>): Promise<T> {
    return this.deps.locks ? this.deps.locks.request(LOCK_NAME, { mode: 'exclusive' }, fn) : fn();
  }

  private async post(path: string, body: unknown, bearer?: string): Promise<Response> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (bearer) headers.Authorization = `Bearer ${bearer}`;
    const request = new Request(new URL(path, this.deps.origin), {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
      signal: this.timeoutSignal(),
    });
    try {
      return await this.deps.fetch(request);
    } catch {
      throw networkError();
    }
  }

  private timeoutSignal(): AbortSignal | undefined {
    return typeof AbortSignal !== 'undefined' && 'timeout' in AbortSignal
      ? AbortSignal.timeout(REQUEST_TIMEOUT_MS)
      : undefined;
  }

  private setState(next: SessionState): void {
    if (next.status === this.state.status && next.endReason === this.state.endReason) return;
    this.state = next;
    for (const l of this.listeners) l(next);
  }
}

/** A refresh answer that retrying can never fix: any 4xx except 429 (TDR-020 §2). */
function isTerminal(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  if (terminalRefreshCodes.has(err.code)) return true;
  return err.status >= 400 && err.status < 500 && err.status !== 429;
}

async function errorOf(res: Response): Promise<ApiError> {
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    // not JSON
  }
  return apiErrorFrom(res.status, body, res.headers.get('X-Request-ID'));
}

/** The browser wiring of a Session. */
export function browserSessionDeps(): SessionDeps {
  return {
    fetch: (input) => globalThis.fetch(input),
    storage: window.localStorage,
    locks: typeof navigator !== 'undefined' && 'locks' in navigator ? (navigator.locks as unknown as LockLike) : undefined,
    now: () => Date.now(),
    setTimer: (fn, ms) => window.setTimeout(fn, ms),
    clearTimer: (h) => window.clearTimeout(h as number),
    onStorageChange: (listener) => {
      const handler = (e: StorageEvent) => {
        if (e.storageArea === window.localStorage) listener(e.key, e.newValue);
      };
      window.addEventListener('storage', handler);
      return () => window.removeEventListener('storage', handler);
    },
    origin: window.location.origin,
  };
}
