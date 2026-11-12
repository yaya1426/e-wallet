// Wallet home (PRD-005 UI-BR-13..15, T031).
import { Link } from 'react-router';
import { useRecentTransactions, useWallet } from '@/api/queries';
import { Alert } from '@/components/Alert';
import { Card, Page } from '@/components/AppShell';
import { CopyButton } from '@/components/CopyButton';
import { DisabledAction } from '@/components/DisabledAction';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { TransactionList } from '@/components/TransactionRow';
import { en } from '@/i18n/en';
import { formatMoney } from '@/lib/money';

export function HomePage() {
  const wallet = useWallet();
  const recent = useRecentTransactions();
  const frozen = wallet.data?.status === 'FROZEN';

  return (
    <Page title={en.home.title}>
      <div className="space-y-4">
        <Card className="bg-brand-50">
          <p id="balance-label" className="text-sm text-slate-600">
            {en.home.availableBalance}
          </p>
          {wallet.isPending && <Spinner small />}
          {wallet.isError && <ErrorWithRetry error={wallet.error} onRetry={() => void wallet.refetch()} />}
          {wallet.data && (
            <p
              data-testid="balance"
              aria-describedby="balance-label"
              className="mt-1 text-3xl font-semibold tracking-tight tabular-nums"
            >
              {formatMoney(wallet.data.available_balance_minor)}
            </p>
          )}
          <QuickActions disabled={frozen} />
        </Card>

        {frozen && <Alert tone="info">{en.home.frozen}</Alert>}

        <Card>
          <div className="mb-2 flex items-center justify-between">
            <h2 className="text-lg font-semibold">{en.home.recent}</h2>
            <Link to="/history" className="text-sm font-medium text-brand-600 hover:underline">
              {en.home.seeAll}
            </Link>
          </div>
          {recent.isPending && <Spinner small />}
          {recent.isError && <ErrorWithRetry error={recent.error} onRetry={() => void recent.refetch()} />}
          {recent.data &&
            (recent.data.items.length === 0 ? (
              <p className="py-4 text-center text-sm text-slate-500">{en.transactions.empty}</p>
            ) : (
              <TransactionList items={recent.data.items} />
            ))}
        </Card>

        {wallet.data && (
          <Card>
            <h2 className="mb-2 text-lg font-semibold">{en.home.account}</h2>
            <p id="wallet-id-label" className="text-sm text-slate-600">
              {en.home.walletId}
            </p>
            <div className="mt-1 flex flex-wrap items-center gap-2">
              <code
                data-testid="wallet-id"
                aria-labelledby="wallet-id-label"
                className="break-all rounded bg-slate-100 px-2 py-1 text-sm"
              >
                {wallet.data.wallet_id}
              </code>
              <CopyButton text={wallet.data.wallet_id} label={en.home.walletId} />
            </div>
          </Card>
        )}
      </div>
    </Page>
  );
}

const actions = [
  { to: '/top-up', label: en.nav.topUp },
  { to: '/withdraw', label: en.nav.withdraw },
  { to: '/send', label: en.nav.send },
];

/** UI-BR-14: on a frozen wallet the actions stay visible (and focusable) but do nothing. */
function QuickActions({ disabled }: { disabled: boolean }) {
  const look =
    'flex-1 rounded-lg px-3 py-2 text-center text-sm font-semibold focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600 focus-visible:ring-offset-2';
  return (
    <div className="mt-4 flex gap-2">
      {actions.map((a) =>
        disabled ? (
          <DisabledAction key={a.to} label={a.label} className={`${look} cursor-not-allowed bg-slate-200 text-slate-500`} />
        ) : (
          <Link key={a.to} to={a.to} className={`${look} bg-brand-600 text-white hover:bg-brand-700`}>
            {a.label}
          </Link>
        ),
      )}
    </div>
  );
}
