// Server state for the operator console. Every key starts with 'ops' so a lock can drop it all.
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { unwrap } from '@/api/client';
import type { components, operations } from '@/api/schema';
import { useOps } from './context';

export type Discrepancy = components['schemas']['Discrepancy'];
export type DiscrepancyStatus = Discrepancy['status'];
export type DiscrepancyKind = NonNullable<NonNullable<operations['listDiscrepancies']['parameters']['query']>['kind']>;
export type OpsHealth = operations['opsHealth']['responses'][200]['content']['application/json'];

export const DISCREPANCY_KINDS: readonly DiscrepancyKind[] = [
  'STATUS_MISMATCH',
  'AMOUNT_MISMATCH',
  'UNMATCHED_BANK_MOVEMENT',
  'UNMATCHED_LEDGER_MOVEMENT',
  'STUCK_TRANSACTION',
  'UNKNOWN_BANK_EVENT',
  'LEDGER_INTEGRITY',
];

export const OPS_HEALTH_REFRESH_MS = 30_000;
export const DISCREPANCY_PAGE = 50;
/** A direct load of a detail page pages through the list at most this far (no GET-by-id API). */
export const DETAIL_MAX_PAGES = 20;

export const opsHealthKey = ['ops', 'health'] as const;
export const discrepancyListKey = (status: DiscrepancyStatus | undefined, kind: DiscrepancyKind | undefined) =>
  ['ops', 'discrepancies', status ?? 'ALL', kind ?? 'ALL'] as const;
export const discrepancyKey = (id: string) => ['ops', 'discrepancy', id] as const;

export function useOpsHealth() {
  const { client } = useOps();
  return useQuery({
    queryKey: opsHealthKey,
    queryFn: async () => unwrap(await client.GET('/internal/v1/health/ops')),
    refetchInterval: OPS_HEALTH_REFRESH_MS,
  });
}

export function useDiscrepancies(status: DiscrepancyStatus | undefined, kind: DiscrepancyKind | undefined) {
  const { client } = useOps();
  return useInfiniteQuery({
    queryKey: discrepancyListKey(status, kind),
    initialPageParam: null as string | null,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await client.GET('/internal/v1/discrepancies', {
          params: { query: { limit: DISCREPANCY_PAGE, ...(status ? { status } : {}), ...(kind ? { kind } : {}), ...(pageParam ? { cursor: pageParam } : {}) } },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor,
  });
}
