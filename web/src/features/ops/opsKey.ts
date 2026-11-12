// TDR-022: the operator key lives in this tab's sessionStorage (and nowhere else) and is
// dropped after 15 minutes without operator activity, on Lock, or when the API rejects it.
export const OPS_STORAGE_KEY = 'ewallet.ops.v1';
export const OPS_IDLE_MS = 15 * 60_000;

export type LockReason = 'locked' | 'idle' | 'rejected';

export interface OpsKeyDeps {
  storage: () => Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | null;
  now: () => number;
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
}

interface Stored {
  key: string;
  lastActive: number;
}

export interface OpsKeySnapshot {
  unlocked: boolean;
  reason: LockReason | null;
}

export class OpsKeyStore {
  private snapshot: OpsKeySnapshot;
  private readonly listeners = new Set<() => void>();
  private timer: unknown = null;

  constructor(private readonly deps: OpsKeyDeps) {
    this.snapshot = { unlocked: false, reason: null };
    if (this.read()) this.snapshot = { unlocked: true, reason: null };
  }

  /** Starts the idle timer (the console is on screen). */
  start(): void {
    this.arm();
  }

  /** Stops the idle timer without dropping the key; the next read still enforces the window. */
  stop(): void {
    if (this.timer !== null) this.deps.clearTimer(this.timer);
    this.timer = null;
  }

  /** The key, or null. A key idle for 15 minutes (e.g. a tab that slept) is dropped here. */
  get(): string | null {
    return this.read()?.key ?? null;
  }

  set(key: string): void {
    this.write({ key, lastActive: this.deps.now() });
    this.arm();
    this.emit({ unlocked: true, reason: null });
  }

  clear(reason: LockReason): void {
    const had = this.storedRaw() !== null || this.snapshot.unlocked;
    try {
      this.deps.storage()?.removeItem(OPS_STORAGE_KEY);
    } catch {
      // Storage unavailable: nothing was stored.
    }
    if (this.timer !== null) this.deps.clearTimer(this.timer);
    this.timer = null;
    if (had) this.emit({ unlocked: false, reason });
  }

  /** Operator activity (click, key press, an action call): restart the idle window. */
  touch(): void {
    const s = this.read();
    if (!s) return;
    this.write({ key: s.key, lastActive: this.deps.now() });
    this.arm();
  }

  getSnapshot = (): OpsKeySnapshot => this.snapshot;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  private read(): Stored | null {
    const raw = this.storedRaw();
    if (!raw) return null;
    let s: Stored;
    try {
      s = JSON.parse(raw) as Stored;
    } catch {
      this.clear('locked');
      return null;
    }
    if (typeof s.key !== 'string' || typeof s.lastActive !== 'number') {
      this.clear('locked');
      return null;
    }
    if (this.deps.now() - s.lastActive >= OPS_IDLE_MS) {
      this.clear('idle');
      return null;
    }
    return s;
  }

  private storedRaw(): string | null {
    try {
      return this.deps.storage()?.getItem(OPS_STORAGE_KEY) ?? null;
    } catch {
      return null;
    }
  }

  private write(s: Stored): void {
    try {
      this.deps.storage()?.setItem(OPS_STORAGE_KEY, JSON.stringify(s));
    } catch {
      // Storage unavailable: the console can't hold a key; the gate stays locked.
    }
  }

  /** Fires when the idle window of the stored key ends; the check in read() makes the decision. */
  private arm(): void {
    if (this.timer !== null) this.deps.clearTimer(this.timer);
    this.timer = null;
    const s = this.read();
    if (!s) return;
    const left = Math.max(0, s.lastActive + OPS_IDLE_MS - this.deps.now());
    this.timer = this.deps.setTimer(() => {
      this.timer = null;
      this.arm(); // drops the key if the window is over, else waits for the rest of it
    }, left);
  }

  private emit(next: OpsKeySnapshot): void {
    this.snapshot = next;
    this.listeners.forEach((fn) => fn());
  }
}

export function browserOpsKeyDeps(): OpsKeyDeps {
  return {
    storage: () => window.sessionStorage,
    now: () => Date.now(),
    setTimer: (fn, ms) => window.setTimeout(fn, ms),
    clearTimer: (h) => window.clearTimeout(h as number),
  };
}
