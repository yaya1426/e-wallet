// Server calls for the send-money flow (T033).
import { useMutation } from '@tanstack/react-query';
import { useCallback } from 'react';
import { unwrap } from '@/api/client';
import type { components } from '@/api/schema';
import { useApi } from '@/auth/AuthProvider';

export type Recipient = components['schemas']['Recipient'];
export type Transfer = components['schemas']['Transfer'];
export type TransferVars = { recipient_phone: string; amount_minor: number; note?: string };

/** GET /v1/recipients?phone= (openapi-fetch encodes "+" as %2B). Called on demand, never cached. */
export function useRecipientPreview() {
  const api = useApi();
  return useMutation({
    mutationFn: async (phone: string): Promise<Recipient> => unwrap(await api.GET('/v1/recipients', { params: { query: { phone } } })),
  });
}

/** POST /v1/transfers with the given Idempotency-Key (TDR-005). */
export function useSendTransfer() {
  const api = useApi();
  return useCallback(
    async (vars: TransferVars, key: string): Promise<Transfer> =>
      unwrap(
        await api.POST('/v1/transfers', {
          params: { header: { 'Idempotency-Key': key } },
          body: { ...vars, currency: 'EGP' },
        }),
      ),
    [api],
  );
}
