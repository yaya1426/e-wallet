import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, NETWORK_ERROR } from '@/api/errors';
import { isRetryable, newIdempotencyKey, STORED_KEY_TTL_MS, useIdempotentRequest } from './useIdempotentRequest';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe('useIdempotentRequest (UI-BR-24)', () => {
  beforeEach(() => window.sessionStorage.clear());

  it('classifies retryable errors', () => {
    expect(isRetryable(new ApiError(0, NETWORK_ERROR))).toBe(true);
    expect(isRetryable(new ApiError(409, 'REQUEST_IN_PROGRESS'))).toBe(true);
    expect(isRetryable(new ApiError(503, 'TRY_AGAIN'))).toBe(true);
    expect(isRetryable(new ApiError(500, 'INTERNAL_ERROR'))).toBe(true);
    expect(isRetryable(new ApiError(422, 'INSUFFICIENT_FUNDS'))).toBe(false);
    expect(isRetryable(new ApiError(409, 'ALREADY_RESOLVED'))).toBe(false);
    expect(isRetryable(new Error('x'))).toBe(false);
  });

  it('makes v4 UUIDs, also without crypto.randomUUID', () => {
    expect(newIdempotencyKey()).toMatch(uuid);
    const original = crypto.randomUUID;
    Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true });
    try {
      expect(newIdempotencyKey()).toMatch(uuid);
    } finally {
      Object.defineProperty(crypto, 'randomUUID', { value: original, configurable: true });
    }
  });

  it('retry after a retryable error reuses the key; changed values get a new one', async () => {
    const send = vi.fn().mockRejectedValueOnce(new ApiError(0, NETWORK_ERROR)).mockResolvedValue('ok');
    const { result } = renderHook(() => useIdempotentRequest('t', send));
    await act(() => result.current.submit(100));
    expect(result.current.state).toMatchObject({ status: 'error', retryable: true });
    await act(() => result.current.retry());
    expect(result.current.state).toEqual({ status: 'success', data: 'ok' });
    expect(send.mock.calls[0]?.[1]).toBe(send.mock.calls[1]?.[1]);
    await act(() => result.current.submit(100));
    expect(send.mock.calls[2]?.[1]).not.toBe(send.mock.calls[1]?.[1]); // after success: a new action
  });

  it('back + same values keeps the key; back + new values drops it', async () => {
    const send = vi.fn().mockRejectedValue(new ApiError(503, 'TRY_AGAIN'));
    const { result } = renderHook(() => useIdempotentRequest('t', send));
    await act(() => result.current.submit(100));
    act(() => result.current.reset());
    expect(result.current.state).toEqual({ status: 'idle' });
    await act(() => result.current.submit(100));
    expect(send.mock.calls[1]?.[1]).toBe(send.mock.calls[0]?.[1]);
    await act(() => result.current.submit(200));
    expect(send.mock.calls[2]?.[1]).not.toBe(send.mock.calls[0]?.[1]);
  });

  it('a non-retryable error drops the key', async () => {
    const send = vi.fn().mockRejectedValue(new ApiError(422, 'INSUFFICIENT_FUNDS'));
    const { result } = renderHook(() => useIdempotentRequest('t', send));
    await act(() => result.current.submit(100));
    expect(result.current.state).toMatchObject({ status: 'error', retryable: false });
    expect(result.current.heldKey()).toBeNull();
    await act(() => result.current.submit(100));
    expect(send.mock.calls[1]?.[1]).not.toBe(send.mock.calls[0]?.[1]);
  });

  it('a second submit while one is in flight is ignored (AC-14)', async () => {
    let resolve: (v: string) => void = () => undefined;
    const send = vi.fn().mockImplementation(() => new Promise<string>((r) => (resolve = r)));
    const { result } = renderHook(() => useIdempotentRequest('t', send));
    let first: Promise<void> = Promise.resolve();
    act(() => {
      first = result.current.submit(100);
      void result.current.submit(100);
    });
    expect(send).toHaveBeenCalledTimes(1);
    await act(async () => {
      resolve('ok');
      await first;
    });
  });

  it('keeps a held key across leaving and coming back (same flow, same values)', async () => {
    const send = vi.fn().mockRejectedValue(new ApiError(0, NETWORK_ERROR));
    const first = renderHook(() => useIdempotentRequest('topUp', send));
    await act(() => first.result.current.submit(100));
    first.unmount();
    const again = renderHook(() => useIdempotentRequest('topUp', send));
    await act(() => again.result.current.submit(100));
    expect(send.mock.calls[1]?.[1]).toBe(send.mock.calls[0]?.[1]);
    const other = renderHook(() => useIdempotentRequest('withdrawal', send));
    await act(() => other.result.current.submit(100));
    expect(send.mock.calls[2]?.[1]).not.toBe(send.mock.calls[0]?.[1]);
  });

  it('a final answer clears the stored key', async () => {
    const send = vi.fn().mockRejectedValueOnce(new ApiError(503, 'TRY_AGAIN')).mockResolvedValue('ok');
    const { result } = renderHook(() => useIdempotentRequest('topUp', send));
    await act(() => result.current.submit(100));
    expect(window.sessionStorage.getItem('ewallet.pendingKey.topUp')).not.toBeNull();
    await act(() => result.current.retry());
    expect(window.sessionStorage.getItem('ewallet.pendingKey.topUp')).toBeNull();
  });

  it('a key stored by an earlier visit expires after 10 minutes (a deliberate repeat gets a new key)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const send = vi.fn().mockRejectedValueOnce(new ApiError(0, NETWORK_ERROR)).mockResolvedValue('ok');
      const first = renderHook(() => useIdempotentRequest('transfer', send));
      await act(() => first.result.current.submit(100));
      first.unmount();
      vi.setSystemTime(Date.now() + STORED_KEY_TTL_MS + 1_000);
      const later = renderHook(() => useIdempotentRequest('transfer', send));
      await act(() => later.result.current.submit(100));
      expect(send.mock.calls[1]?.[1]).not.toBe(send.mock.calls[0]?.[1]);
    } finally {
      vi.useRealTimers();
    }
  });
});
