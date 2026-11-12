import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { browserOpsKeyDeps, OPS_IDLE_MS, OPS_STORAGE_KEY, OpsKeyStore } from './opsKey';

describe('opsKey (T034 AC-1, TDR-022)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('set stores in sessionStorage only; clear removes it with a reason', () => {
    const store = new OpsKeyStore(browserOpsKeyDeps());
    const seen: unknown[] = [];
    store.subscribe(() => seen.push(store.getSnapshot()));
    store.set('k-1');
    expect(store.get()).toBe('k-1');
    expect(JSON.parse(window.sessionStorage.getItem(OPS_STORAGE_KEY) ?? '{}')).toMatchObject({ key: 'k-1' });
    expect(window.localStorage.length).toBe(0);
    store.clear('locked');
    expect(store.get()).toBeNull();
    expect(window.sessionStorage.getItem(OPS_STORAGE_KEY)).toBeNull();
    expect(seen).toEqual([
      { unlocked: true, reason: null },
      { unlocked: false, reason: 'locked' },
    ]);
  });

  it('locks after 15 minutes without activity; activity restarts the window', () => {
    const store = new OpsKeyStore(browserOpsKeyDeps());
    store.start();
    store.set('k');
    vi.advanceTimersByTime(OPS_IDLE_MS - 60_000);
    store.touch(); // operator clicked
    vi.advanceTimersByTime(OPS_IDLE_MS - 60_000);
    expect(store.getSnapshot()).toEqual({ unlocked: true, reason: null });
    vi.advanceTimersByTime(60_000);
    expect(store.getSnapshot()).toEqual({ unlocked: false, reason: 'idle' });
    expect(window.sessionStorage.getItem(OPS_STORAGE_KEY)).toBeNull();
  });

  it('reads (like auto-refresh) do not count as activity', () => {
    const store = new OpsKeyStore(browserOpsKeyDeps());
    store.start();
    store.set('k');
    for (let i = 0; i < 29; i++) {
      vi.advanceTimersByTime(30_000);
      store.get();
    }
    vi.advanceTimersByTime(30_000);
    expect(store.getSnapshot().reason).toBe('idle');
  });

  it('a key left idle in storage (tab asleep, page reload) is not used', () => {
    window.sessionStorage.setItem(OPS_STORAGE_KEY, JSON.stringify({ key: 'old', lastActive: Date.now() - OPS_IDLE_MS - 1 }));
    const store = new OpsKeyStore(browserOpsKeyDeps());
    expect(store.getSnapshot().unlocked).toBe(false);
    expect(store.get()).toBeNull();
    window.sessionStorage.setItem(OPS_STORAGE_KEY, JSON.stringify({ key: 'fresh', lastActive: Date.now() }));
    expect(new OpsKeyStore(browserOpsKeyDeps()).get()).toBe('fresh');
  });

  it('a corrupt stored value is dropped', () => {
    window.sessionStorage.setItem(OPS_STORAGE_KEY, '{nope');
    expect(new OpsKeyStore(browserOpsKeyDeps()).get()).toBeNull();
    expect(window.sessionStorage.getItem(OPS_STORAGE_KEY)).toBeNull();
  });
});
