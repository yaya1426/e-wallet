// Shared server-state hooks for the wallet screens (T031). Feature folders use these instead of
// importing each other (TDR-019 §2).
import { useInfiniteQuery, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useRef } from 'react';
import { useApi } from '@/auth/AuthProvider';
import { isPending, type TransactionItem } from '@/lib/transactions';
import { unwrap } from './client';
import type { components } from './schema';

export type Wallet = components['schemas']['Wallet'];
export type TransactionPage = components['schemas']['TransactionPage'];

export const queryKeys = {
  wallet: ['me', 'wallet'] as const,
  transactions: ['me', 'transactions'] as const,
  recentTransactions: ['me', 'transactions', 'recent'] as const,
  transactionList: ['me', 'transactions', 'list'] as const,
  transaction: (id: string) => ['transaction', id] as const,
};

/** Refetch everything that shows money after a money action (PRD-005 UI-BR-19). */
export async function invalidateMoney(queryClient: QueryClient): Promise<void> {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.wallet }),
    queryClient.invalidateQueries({ queryKey: queryKeys.transactions }),
  ]);
}

export const RECENT_LIMIT = 5;
export const PAGE_SIZE = 20;
export const PENDING_POLL_MS = 5_000;
export const PENDING_POLL_MAX_MS = 120_000;

/**
 * UI-BR-19: while something on screen is still pending, poll every `everyMs` for at most `forMs`
 * after it was first seen pending (the window restarts once nothing is pending). Returns a `refetchInterval` callback for TanStack Query.
 */
export function usePendingPoll(everyMs = PENDING_POLL_MS, forMs = PENDING_POLL_MAX_MS) {
  // The window starts the first time something pending is seen (i.e. right after it loads).
  const startedAt = useRef<number | null>(null);
  return (anyPending: boolean): number | false => {
    if (!anyPending) {
      startedAt.current = null; // a later pending item gets its own window
      return false;
    }
    const now = Date.now();
    startedAt.current ??= now;
    return now - startedAt.current < forMs ? everyMs : false;
  };
}

/**
 * When a transaction on screen goes from pending to final, the balance changed (or a reserved
 * withdrawal was refunded): refetch the wallet so the screen never contradicts itself (UI-BR-19).
 */
export function useRefreshWalletWhenSettled(items: readonly Pick<TransactionItem, 'id' | 'status'>[] | undefined) {
  const queryClient = useQueryClient();
  const pending = useRef(new Set<string>());
  useEffect(() => {
    if (!items) return;
    let settled = false;
    const nowPending = new Set<string>();
    for (const t of items) {
      if (isPending(t.status)) nowPending.add(t.id);
      else if (pending.current.has(t.id)) settled = true;
    }
    pending.current = nowPending;
    if (settled) void queryClient.invalidateQueries({ queryKey: queryKeys.wallet });
  }, [items, queryClient]);
}

export function useWallet() {
  const api = useApi();
  return useQuery({
    queryKey: queryKeys.wallet,
    queryFn: async () => unwrap(await api.GET('/v1/me/wallet')),
    refetchOnWindowFocus: true,
  });
}

export function useRecentTransactions() {
  const api = useApi();
  const poll = usePendingPoll();
  const query = useQuery({
    queryKey: queryKeys.recentTransactions,
    queryFn: async () => unwrap(await api.GET('/v1/me/transactions', { params: { query: { limit: RECENT_LIMIT } } })),
    refetchOnWindowFocus: true,
    refetchInterval: (q) => poll(q.state.data?.items.some((t) => isPending(t.status)) ?? false),
  });
  useRefreshWalletWhenSettled(query.data?.items);
  return query;
}

export function useTransactionList() {
  const api = useApi();
  const poll = usePendingPoll();
  const query = useInfiniteQuery({
    queryKey: queryKeys.transactionList,
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET('/v1/me/transactions', {
          params: { query: pageParam ? { limit: PAGE_SIZE, cursor: pageParam } : { limit: PAGE_SIZE } },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchOnWindowFocus: true,
    refetchInterval: (q) =>
      poll(q.state.data?.pages.some((p) => p.items.some((t) => isPending(t.status))) ?? false),
  });
  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items), [query.data]);
  useRefreshWalletWhenSettled(items);
  return { ...query, items };
}

/** One transaction; `pollEveryMs`/`pollForMs` let the top-up/withdrawal status screen poll faster. */
export function useTransaction(id: string, opts: { pollEveryMs?: number; pollForMs?: number } = {}) {
  const api = useApi();
  const poll = usePendingPoll(opts.pollEveryMs, opts.pollForMs);
  const query = useQuery({
    queryKey: queryKeys.transaction(id),
    queryFn: async () =>
      unwrap(await api.GET('/v1/transactions/{id}', { params: { path: { id } } })) as TransactionItem,
    refetchOnWindowFocus: true,
    refetchInterval: (q) => poll(q.state.data ? isPending(q.state.data.status) : false),
  });
  const items = useMemo(() => (query.data ? [query.data] : undefined), [query.data]);
  useRefreshWalletWhenSettled(items);
  return query;
}
