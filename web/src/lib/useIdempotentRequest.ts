// PRD-005 UI-BR-24 / TDR-005: one Idempotency-Key per user action. A retryable failure keeps the
// key so "Try again" can never charge twice; any other final answer drops it. The held key is
// also kept in sessionStorage (per flow) so leaving the page after an ambiguous failure and
// coming back with the same request still reuses it.
import { useCallback, useRef, useState } from 'react';
import { isApiError, NETWORK_ERROR, type ApiError } from '@/api/errors';

export type RequestState<R> =
  | { status: 'idle' }
  | { status: 'pending' }
  | { status: 'success'; data: R }
  | { status: 'error'; error: unknown; retryable: boolean };

/** Network errors, 409 REQUEST_IN_PROGRESS, 503 TRY_AGAIN and any 5xx: same request, same key. */
export function isRetryable(err: unknown): boolean {
  if (!isApiError(err)) return false;
  const e: ApiError = err;
  return e.code === NETWORK_ERROR || e.code === 'REQUEST_IN_PROGRESS' || e.status >= 500;
}

export function newIdempotencyKey(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  // Fallback for non-secure contexts: RFC 4122 v4 from getRandomValues.
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x40;
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

interface Held<V> {
  key: string;
  vars: V;
  json: string;
  /** When the key was made (ms). A key left in storage by an earlier visit expires (see below). */
  at: number;
}

/**
 * A key kept in sessionStorage is reused by a later visit only for this long: long enough to
 * retry an ambiguous failure after a reload, short enough that a deliberate identical repeat
 * (common for P2P) later in the day is not silently answered with the old replay.
 */
export const STORED_KEY_TTL_MS = 10 * 60_000;

const storageKey = (scope: string) => `ewallet.pendingKey.${scope}`;

function readStored<V>(scope: string): Held<V> | null {
  try {
    const raw = window.sessionStorage.getItem(storageKey(scope));
    if (!raw) return null;
    const held = JSON.parse(raw) as Held<V>;
    if (typeof held.at !== 'number' || Date.now() - held.at > STORED_KEY_TTL_MS) return null;
    return held;
  } catch {
    return null;
  }
}

function writeStored<V>(scope: string, held: Held<V> | null): void {
  try {
    if (held) window.sessionStorage.setItem(storageKey(scope), JSON.stringify(held));
    else window.sessionStorage.removeItem(storageKey(scope));
  } catch {
    // Storage unavailable: the in-memory key still protects retries on this page.
  }
}

/**
 * `scope` names the flow (e.g. "topUp"); a key held for one flow is never used by another.
 * `vars` must be JSON-serialisable; the key is reused only for exactly the same vars.
 */
export function useIdempotentRequest<V, R>(scope: string, send: (vars: V, key: string) => Promise<R>) {
  const [state, setState] = useState<RequestState<R>>({ status: 'idle' });
  const held = useRef<Held<V> | null>(null);
  const inFlight = useRef(false);

  const hold = useCallback(
    (next: Held<V> | null) => {
      held.current = next;
      writeStored(scope, next);
    },
    [scope],
  );

  const run = useCallback(
    async (vars: V, key: string): Promise<void> => {
      inFlight.current = true;
      setState({ status: 'pending' });
      try {
        const data = await send(vars, key);
        hold(null);
        setState({ status: 'success', data });
      } catch (error) {
        const retryable = isRetryable(error);
        if (!retryable) hold(null);
        setState({ status: 'error', error, retryable });
      } finally {
        inFlight.current = false;
      }
    },
    [send, hold],
  );

  /** Sends `vars`; reuses the held key only when the values are exactly the same. */
  const submit = useCallback(
    (vars: V) => {
      if (inFlight.current) return Promise.resolve(); // a double click sends one request
      const json = JSON.stringify(vars);
      const current = held.current ?? readStored<V>(scope);
      if (current && current.json === json) {
        held.current = current;
      } else {
        hold({ key: newIdempotencyKey(), vars, json, at: Date.now() });
      }
      const h = held.current as Held<V>;
      return run(h.vars, h.key);
    },
    [run, hold, scope],
  );

  /** Resends the held request with the same key (after a retryable error). */
  const retry = useCallback(() => {
    if (inFlight.current || !held.current) return Promise.resolve();
    return run(held.current.vars, held.current.key);
  }, [run]);

  /**
   * Forget the last answer (the user is editing). A key held after a retryable failure is kept,
   * and `submit` reuses it if the values come back unchanged; changed values get a new key.
   */
  const reset = useCallback(() => {
    if (inFlight.current) return;
    setState({ status: 'idle' });
  }, []);

  return { state, submit, retry, reset, heldKey: () => held.current?.key ?? null };
}
