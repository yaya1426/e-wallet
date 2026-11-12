// Server state for the bank account, top-up and withdrawal screens (T032).
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { unwrap } from '@/api/client';
import { isApiError } from '@/api/errors';
import type { components } from '@/api/schema';
import { useApi } from '@/auth/AuthProvider';

export type BankAccount = components['schemas']['BankAccount'];
export type MoneyKind = 'topUp' | 'withdrawal';

export const bankAccountKey = ['me', 'bankAccount'] as const;

/** The linked account, or `null` when none is linked (404 BANK_ACCOUNT_NOT_FOUND). */
export function useBankAccount() {
  const api = useApi();
  return useQuery({
    queryKey: bankAccountKey,
    queryFn: async (): Promise<BankAccount | null> => {
      try {
        return unwrap(await api.GET('/v1/me/bank-account'));
      } catch (err) {
        if (isApiError(err) && err.code === 'BANK_ACCOUNT_NOT_FOUND') return null;
        throw err;
      }
    },
  });
}

export function useLinkBankAccount() {
  const api = useApi();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: { iban: string; holder_name: string }) =>
      unwrap(await api.PUT('/v1/me/bank-account', { body })),
    onSuccess: (account) => queryClient.setQueryData(bankAccountKey, account),
  });
}

/** POST /v1/topups or /v1/withdrawals with the given Idempotency-Key (TDR-005). */
export function useSendMoney(kind: MoneyKind) {
  const api = useApi();
  return async (amountMinor: number, key: string) => {
    const init = {
      params: { header: { 'Idempotency-Key': key } },
      body: { amount_minor: amountMinor, currency: 'EGP' as const },
    };
    const result = kind === 'topUp' ? await api.POST('/v1/topups', init) : await api.POST('/v1/withdrawals', init);
    return unwrap(result);
  };
}
