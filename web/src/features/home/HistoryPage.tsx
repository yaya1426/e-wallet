// Transaction history with cursor pagination (PRD-005 UI-BR-16, T031).
import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { isApiError } from '@/api/errors';
import { queryKeys, useTransactionList } from '@/api/queries';
import { Card, Page } from '@/components/AppShell';
import { Button } from '@/components/Button';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { TransactionList } from '@/components/TransactionRow';
import { en } from '@/i18n/en';

export function HistoryPage() {
  const list = useTransactionList();
  const queryClient = useQueryClient();
  const items = list.items ?? [];

  // A cursor the API no longer accepts (should not happen): start again from the first page.
  const badCursor = isApiError(list.error) && list.error.code === 'INVALID_CURSOR';
  useEffect(() => {
    if (badCursor) void queryClient.resetQueries({ queryKey: queryKeys.transactionList });
  }, [badCursor, queryClient]);

  // Already loaded rows stay; Retry re-runs whatever failed (the next page, or a refetch).
  const retry = () => void (list.isFetchNextPageError ? list.fetchNextPage() : list.refetch());

  return (
    <Page title={en.transactions.historyTitle}>
      <Card>
        {list.isPending && <Spinner small />}
        {list.data && items.length === 0 && (
          <p className="py-4 text-center text-sm text-slate-500">{en.transactions.empty}</p>
        )}
        {items.length > 0 && <TransactionList items={items} />}
        {list.isError && !badCursor && <ErrorWithRetry error={list.error} onRetry={retry} />}
        {list.hasNextPage && !list.isError && (
          <div className="mt-3">
            <Button
              type="button"
              variant="secondary"
              loading={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {en.transactions.loadMore}
            </Button>
          </div>
        )}
      </Card>
    </Page>
  );
}
