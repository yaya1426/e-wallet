// UI-BR-32: what needs an operator, refreshed every 30 s (refreshes don't count as activity).
import { useState } from 'react';
import { Link } from 'react-router';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { Spinner } from '@/components/Spinner';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { OpsPage } from './OpsLayout';
import { OpsErrorWithRetry } from './opsErrors';
import { useOpsHealth, type OpsHealth } from './queries';

const t = en.ops.health;

const pillLooks: Record<OpsHealth['status'], string> = {
  ok: 'bg-emerald-100 text-emerald-900 border-emerald-300',
  warning: 'bg-amber-100 text-amber-900 border-amber-300',
  critical: 'bg-red-100 text-red-900 border-red-300',
};
const pillIcon: Record<OpsHealth['status'], string> = { ok: '✓', warning: '!', critical: '✕' };

export function OpsHealthPage() {
  const health = useOpsHealth();
  // Only a refresh the operator asked for shows as loading (auto-refresh must not steal focus).
  const [manual, setManual] = useState(false);
  const refresh = (
    <Button
      type="button"
      variant="secondary"
      className="sm:w-auto"
      onClick={() => {
        setManual(true);
        void health.refetch().finally(() => setManual(false));
      }}
      loading={manual}
    >
      {t.refresh}
    </Button>
  );
  return (
    <OpsPage title={t.title} actions={health.data ? refresh : undefined}>
      {health.isPending ? (
        <Spinner small />
      ) : health.data === undefined ? (
        <OpsErrorWithRetry error={health.error} onRetry={() => void health.refetch()} />
      ) : (
        <div className="space-y-4">
          {health.isRefetchError && <Alert tone="error">{t.refreshFailed}</Alert>}
          <HealthBody data={health.data} checkedAt={health.dataUpdatedAt} />
        </div>
      )}
    </OpsPage>
  );
}

function HealthBody({ data, checkedAt }: { data: OpsHealth; checkedAt: number }) {
  const openCritical = data.discrepancies.open_critical ?? 0;
  const openWarning = data.discrepancies.open_warning ?? 0;
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm text-slate-600">{t.statusLabel}</span>
        <span data-testid="ops-status" className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-sm font-semibold ${pillLooks[data.status]}`}>
          <span aria-hidden="true">{pillIcon[data.status]}</span>
          {t.status[data.status] ?? data.status}
        </span>
        <span className="text-sm text-slate-500">
          {t.lastChecked(formatDateTime(new Date(checkedAt).toISOString()))}
        </span>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <CountCard
          title={t.discrepancies}
          rows={[
            [t.critical, openCritical],
            [t.warning, openWarning],
          ]}
          footer={
            openCritical + openWarning > 0 ? (
              <Link to="/ops/discrepancies?status=OPEN" className="text-sm font-medium text-brand-600 hover:underline">
                {t.viewOpen}
              </Link>
            ) : null
          }
        />
        <CountCard
          title={t.stuck}
          rows={[
            [t.topUps, data.stuck.TOPUP ?? 0],
            [t.withdrawals, data.stuck.WITHDRAWAL ?? 0],
          ]}
        />
        <CountCard
          title={t.outbox}
          rows={[
            [t.pending, data.outbox.pending ?? 0],
            [t.dead, data.outbox.dead ?? 0],
          ]}
        />
        <CountCard
          title={t.inbox}
          rows={[
            [t.pending, data.inbox.pending ?? 0],
            [t.dead, data.inbox.dead ?? 0],
          ]}
        />
      </div>

      <div className="overflow-x-auto rounded-2xl border border-slate-200 bg-white shadow-sm">
        <table className="w-full text-left text-sm">
          <caption className="px-4 pt-3 text-left font-semibold">{t.jobs}</caption>
          <thead className="text-slate-500">
            <tr>
              <th scope="col" className="px-4 py-2 font-medium">{t.job}</th>
              <th scope="col" className="px-4 py-2 font-medium">{t.lastStatus}</th>
              <th scope="col" className="px-4 py-2 font-medium">{t.lastFinished}</th>
              <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">{t.late}</span></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {data.jobs.map((job) => (
              <tr key={job.name}>
                <td className="px-4 py-2 font-mono text-xs">{job.name}</td>
                <td className="px-4 py-2">{job.last_status ?? t.never}</td>
                <td className="px-4 py-2 whitespace-nowrap">{job.last_finished_at ? formatDateTime(job.last_finished_at) : t.never}</td>
                <td className="px-4 py-2">
                  {job.late ? (
                    <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-900">{t.late}</span>
                  ) : (
                    <span className="text-xs text-slate-500">{t.onTime}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function CountCard({ title, rows, footer }: { title: string; rows: [string, number][]; footer?: React.ReactNode }) {
  return (
    <section className="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm" aria-label={title}>
      <h2 className="mb-2 text-sm font-semibold text-slate-700">{title}</h2>
      <dl className="space-y-1">
        {rows.map(([label, n]) => (
          <div key={label} className="flex justify-between text-sm">
            <dt className="text-slate-600">{label}</dt>
            <dd className={`tabular-nums font-semibold ${n > 0 ? 'text-slate-900' : 'text-slate-400'}`}>{n}</dd>
          </div>
        ))}
      </dl>
      {footer && <div className="mt-2">{footer}</div>}
    </section>
  );
}
