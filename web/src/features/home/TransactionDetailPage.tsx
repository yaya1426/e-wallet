// One transaction from the user's point of view (PRD-005 UI-BR-18, T031).
import type { ReactNode } from 'react';
import { Link, useParams } from 'react-router';
import { isApiError } from '@/api/errors';
import { useTransaction } from '@/api/queries';
import { Alert } from '@/components/Alert';
import { Card, Page } from '@/components/AppShell';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { StatusBadge } from '@/components/StatusBadge';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { formatPhone } from '@/lib/phone';
import { failureMessage, signedAmount, transactionTitle, typeLabel } from '@/lib/transactions';

export function TransactionDetailPage() {
  const { id = '' } = useParams();
  const tx = useTransaction(id);

  return (
    <Page title={en.transactions.detailTitle}>
      <Card>
        {tx.isPending && <Spinner small />}
        {tx.isError &&
          (isApiError(tx.error) && tx.error.code === 'TRANSACTION_NOT_FOUND' ? (
            <div className="space-y-3">
              <Alert tone="error">{en.transactions.notFound}</Alert>
              <BackLink />
            </div>
          ) : (
            <ErrorWithRetry error={tx.error} onRetry={() => void tx.refetch()} />
          ))}
        {tx.data && (
          <>
            <p className="text-lg font-semibold">{transactionTitle(tx.data)}</p>
            <p data-testid="detail-amount" className="mb-4 text-3xl font-semibold tabular-nums">
              {tx.data.status === 'FAILED' ? (
                <>
                  <s>{signedAmount(tx.data)}</s>
                  <span className="sr-only"> {en.transactions.noMoneyMoved}</span>
                </>
              ) : (
                signedAmount(tx.data)
              )}
            </p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <Row label={en.transactions.fields.type}>
                {typeLabel(tx.data.type)} · {en.transactions.directions[tx.data.direction]}
              </Row>
              <Row label={en.transactions.fields.status}>
                <StatusBadge status={tx.data.status} />
              </Row>
              {tx.data.counterparty && (
                <Row label={en.transactions.fields.counterparty}>
                  {tx.data.counterparty.masked_name} ({formatPhone(tx.data.counterparty.phone)})
                </Row>
              )}
              {tx.data.note && <Row label={en.transactions.fields.note}>{tx.data.note}</Row>}
              <Row label={en.transactions.fields.created}>{formatDateTime(tx.data.created_at)}</Row>
              {tx.data.completed_at && (
                <Row label={en.transactions.fields.completed}>{formatDateTime(tx.data.completed_at)}</Row>
              )}
              <Row label={en.transactions.fields.reference}>
                <code className="break-all text-xs">{tx.data.id}</code>
              </Row>
            </dl>
            {tx.data.status === 'FAILED' && (
              <div className="mt-4">
                <Alert tone="info">
                  {failureMessage(tx.data.failure_code)}
                  {tx.data.type === 'WITHDRAWAL' && <> {en.transactions.refunded}</>}
                </Alert>
              </div>
            )}
            <div className="mt-4">
              <BackLink />
            </div>
          </>
        )}
      </Card>
    </Page>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-slate-500">{label}</dt>
      <dd className="min-w-0 text-slate-900">{children}</dd>
    </>
  );
}

function BackLink() {
  return (
    <Link to="/history" className="text-sm font-medium text-brand-600 hover:underline">
      {en.transactions.backToHistory}
    </Link>
  );
}
