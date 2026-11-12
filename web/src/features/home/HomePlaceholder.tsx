// Placeholder home (T030): proves the session works end to end. Replaced by area 2 (T031).
import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { unwrap } from '@/api/client';
import { useApi, useAuth } from '@/auth/AuthProvider';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { PageLayout } from '@/components/PageLayout';
import { Spinner } from '@/components/Spinner';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { formatMoney } from '@/lib/money';

export function HomePlaceholder() {
  const { session } = useAuth();
  const api = useApi();
  const [signingOut, setSigningOut] = useState(false);
  const signOut = () => {
    setSigningOut(true);
    void session.logout();
  };
  const wallet = useQuery({
    queryKey: ['me', 'wallet'],
    queryFn: async () => unwrap(await api.GET('/v1/me/wallet')),
  });

  return (
    <PageLayout title={en.home.title}>
      <section aria-labelledby="balance-label" className="mb-6 rounded-xl bg-brand-50 p-5">
        <p id="balance-label" className="text-sm text-slate-600">
          {en.home.availableBalance}
        </p>
        {wallet.isPending && <Spinner small />}
        {wallet.isError && (
          <div className="mt-2 space-y-2">
            <Alert tone="error">{messageForError(wallet.error)}</Alert>
            <Button type="button" variant="secondary" onClick={() => void wallet.refetch()}>
              {en.common.retry}
            </Button>
          </div>
        )}
        {wallet.data && (
          <p data-testid="balance" className="mt-1 text-3xl font-semibold tracking-tight">
            {formatMoney(wallet.data.available_balance_minor)}
          </p>
        )}
      </section>
      {wallet.data?.status === 'FROZEN' && (
        <div className="mb-6">
          <Alert tone="info">{en.home.frozen}</Alert>
        </div>
      )}
      <Button type="button" variant="secondary" loading={signingOut} onClick={signOut}>
        {en.home.signOut}
      </Button>
    </PageLayout>
  );
}
