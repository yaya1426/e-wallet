// UI-BR-34: discrepancy list with status/kind filters kept in the URL.
import { Link, useSearchParams } from 'react-router';
import { Button } from '@/components/Button';
import { Spinner } from '@/components/Spinner';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { OpsPage } from './OpsLayout';
import { OpsErrorWithRetry } from './opsErrors';
import { DISCREPANCY_KINDS, useDiscrepancies, type DiscrepancyKind, type DiscrepancyStatus } from './queries';

const t = en.ops.discrepancies;

/** `?status=` OPEN (default), RESOLVED or ALL; `?kind=` one of the enum values or empty. */
export function readFilters(params: URLSearchParams): { status: DiscrepancyStatus | undefined; kind: DiscrepancyKind | undefined } {
  const s = params.get('status');
  const k = params.get('kind');
  return {
    status: s === 'RESOLVED' ? 'RESOLVED' : s === 'ALL' ? undefined : 'OPEN',
    kind: DISCREPANCY_KINDS.includes(k as DiscrepancyKind) ? (k as DiscrepancyKind) : undefined,
  };
}

export function DiscrepanciesPage() {
  const [params, setParams] = useSearchParams();
  const { status, kind } = readFilters(params);
  const list = useDiscrepancies(status, kind);
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];

  const setFilter = (name: 'status' | 'kind', value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(name, value);
    else next.delete(name);
    setParams(next, { replace: true });
  };

  return (
    <OpsPage title={t.title}>
      <div className="mb-4 flex flex-wrap gap-4">
        <label className="text-sm">
          <span className="mb-1 block font-medium text-slate-700">{t.status}</span>
          <select
            value={status ?? 'ALL'}
            onChange={(e) => setFilter('status', e.target.value)}
            className="rounded-lg border border-slate-300 bg-white px-3 py-2 focus:outline-none focus:ring-2 focus:ring-brand-600"
          >
            {(['OPEN', 'RESOLVED', 'ALL'] as const).map((s) => (
              <option key={s} value={s}>
                {t.statuses[s]}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block font-medium text-slate-700">{t.kind}</span>
          <select
            value={kind ?? ''}
            onChange={(e) => setFilter('kind', e.target.value)}
            className="rounded-lg border border-slate-300 bg-white px-3 py-2 focus:outline-none focus:ring-2 focus:ring-brand-600"
          >
            <option value="">{t.allKinds}</option>
            {DISCREPANCY_KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </label>
      </div>

      {list.isPending ? (
        <Spinner small />
      ) : list.data === undefined ? (
        <OpsErrorWithRetry error={list.error} onRetry={() => void list.refetch()} />
      ) : items.length === 0 ? (
        <p className="text-slate-600">{t.empty}</p>
      ) : (
        <div className="space-y-3">
          <div className="overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
            <table className="w-full text-left text-sm">
              <thead className="text-slate-500">
                <tr>
                  <th scope="col" className="px-4 py-2 font-medium">{t.created}</th>
                  <th scope="col" className="px-4 py-2 font-medium">{t.severity}</th>
                  <th scope="col" className="px-4 py-2 font-medium">{t.kind}</th>
                  <th scope="col" className="px-4 py-2 font-medium">{t.transaction}</th>
                  <th scope="col" className="px-4 py-2 font-medium">{t.status}</th>
                  <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">{t.view}</span></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {items.map((d) => (
                  <tr key={d.id} data-testid="discrepancy-row">
                    <td className="px-4 py-2 whitespace-nowrap">{formatDateTime(d.created_at)}</td>
                    <td className="px-4 py-2">
                      <SeverityBadge severity={d.severity} />
                    </td>
                    <td className="px-4 py-2 font-mono text-xs">{d.kind}</td>
                    <td className="px-4 py-2 font-mono text-xs" title={d.transaction_id ?? undefined}>
                      {d.transaction_id ? `${d.transaction_id.slice(0, 8)}…` : '—'}
                    </td>
                    <td className="px-4 py-2">{t.statuses[d.status]}</td>
                    <td className="px-4 py-2 text-right">
                      <Link to={`/ops/discrepancies/${d.id}`} className="font-medium text-brand-600 hover:underline">
                        {t.view}
                        <span className="sr-only"> {d.kind} {formatDateTime(d.created_at)}</span>
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {list.isFetchNextPageError && <OpsErrorWithRetry error={list.error} onRetry={() => void list.fetchNextPage()} />}
          {list.hasNextPage && !list.isFetchNextPageError && (
            <Button type="button" variant="secondary" className="sm:w-auto" loading={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
              {t.loadMore}
            </Button>
          )}
        </div>
      )}
    </OpsPage>
  );
}

export function SeverityBadge({ severity }: { severity: 'CRITICAL' | 'WARNING' }) {
  const look = severity === 'CRITICAL' ? 'bg-red-100 text-red-900' : 'bg-amber-100 text-amber-900';
  return <span className={`rounded-full px-2 py-0.5 text-xs font-semibold ${look}`}>{severity === 'CRITICAL' ? en.ops.health.critical : en.ops.health.warning}</span>;
}
