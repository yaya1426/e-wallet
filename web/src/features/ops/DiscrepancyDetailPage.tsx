// UI-BR-34: one discrepancy and its resolve form. There is no GET-by-id API (T034 Q4): the item
// comes from a loaded list, or on a direct load is found by paging through the full list.
import { useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { Link, useParams } from 'react-router';
import { unwrap } from '@/api/client';
import { isApiError } from '@/api/errors';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { CopyButton } from '@/components/CopyButton';
import { Spinner } from '@/components/Spinner';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { isUuid, validateResolutionNote } from '@/lib/validation';
import { useOps } from './context';
import { SeverityBadge } from './DiscrepanciesPage';
import { OpsPage } from './OpsLayout';
import { OpsErrorWithRetry, opsMessage } from './opsErrors';
import { DETAIL_MAX_PAGES, DISCREPANCY_PAGE, discrepancyKey, opsHealthKey, type Discrepancy } from './queries';

const t = en.ops.discrepancies;
type Page = { items: Discrepancy[]; next_cursor: string | null };

export function DiscrepancyDetailPage() {
  const { id = '' } = useParams();
  const { client } = useOps();
  const queryClient = useQueryClient();
  const [notice, setNotice] = useState<{ tone: 'success' | 'error'; text: string } | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  // The newest copy of this item in any loaded list, with when that list was fetched.
  const listed = (() => {
    let best: { item: Discrepancy; at: number } | undefined;
    for (const q of queryClient.getQueryCache().findAll({ queryKey: ['ops', 'discrepancies'] })) {
      const data = q.state.data as InfiniteData<Page> | undefined;
      const item = data?.pages.flatMap((p) => p.items).find((d) => d.id === id);
      if (item && (!best || q.state.dataUpdatedAt > best.at)) best = { item, at: q.state.dataUpdatedAt };
    }
    return best;
  })();

  const detail = useQuery({
    queryKey: discrepancyKey(id),
    initialData: listed?.item,
    initialDataUpdatedAt: listed?.at,
    queryFn: async (): Promise<Discrepancy | null> => {
      if (!isUuid(id)) return null;
      let cursor: string | null = null;
      for (let page = 0; page < DETAIL_MAX_PAGES; page++) {
        const res: Page = unwrap(
          await client.GET('/internal/v1/discrepancies', { params: { query: { limit: DISCREPANCY_PAGE, ...(cursor ? { cursor } : {}) } } }),
        );
        const hit = res.items.find((d) => d.id === id);
        if (hit) return hit;
        if (!res.next_cursor) break;
        cursor = res.next_cursor;
      }
      return null;
    },
    staleTime: Infinity, // kept current by the lists (below) and the resolve answer
  });
  // A list fetched after this item was loaded knows better (e.g. resolved meanwhile).
  const shown = listed && listed.at > detail.dataUpdatedAt ? listed.item : detail.data;

  const back = (
    <Link to="/ops/discrepancies" className="text-sm font-medium text-brand-600 hover:underline">
      {t.back}
    </Link>
  );

  let body: React.ReactNode;
  if (shown === undefined && detail.isPending) body = <Spinner small />;
  else if (shown === undefined) body = <OpsErrorWithRetry error={detail.error} onRetry={() => void detail.refetch()} />;
  else if (shown === null) body = <Alert tone="error">{t.notFound}</Alert>;
  else
    body = (
      <DiscrepancyView
        d={shown}
        onResolved={(d) => {
          queryClient.setQueryData(discrepancyKey(id), d);
          setNotice({ tone: 'success', text: t.resolved });
          void queryClient.invalidateQueries({ queryKey: ['ops', 'discrepancies'] });
          void queryClient.invalidateQueries({ queryKey: opsHealthKey });
        }}
        onStale={() => {
          // Reload the item (it will show as resolved) and keep the message above it.
          setNotice({ tone: 'error', text: t.alreadyResolved });
          void queryClient.invalidateQueries({ queryKey: ['ops', 'discrepancies'] });
          void queryClient.invalidateQueries({ queryKey: opsHealthKey });
          void detail.refetch();
        }}
        onGone={() => queryClient.setQueryData(discrepancyKey(id), null)}
      />
    );

  return (
    <OpsPage title={t.title} actions={back}>
      {notice && (
        <div ref={noticeRef} tabIndex={-1} className="mb-4 focus:outline-none">
          <Alert tone={notice.tone}>{notice.text}</Alert>
        </div>
      )}
      {body}
    </OpsPage>
  );
}

interface ViewProps {
  d: Discrepancy;
  onResolved: (d: Discrepancy) => void;
  onStale: () => void;
  onGone: () => void;
}

function DiscrepancyView({ d, onResolved, onStale, onGone }: ViewProps) {
  return (
    <div className="space-y-4">
      <div className="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm sm:p-5">
        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[10rem_1fr]">
          <dt className="text-slate-500">{t.kind}</dt>
          <dd className="font-mono text-xs">{d.kind}</dd>
          <dt className="text-slate-500">{t.severity}</dt>
          <dd>
            <SeverityBadge severity={d.severity} />
          </dd>
          <dt className="text-slate-500">{t.status}</dt>
          <dd data-testid="discrepancy-status">{t.statuses[d.status]}</dd>
          <dt className="text-slate-500">{t.created}</dt>
          <dd>{formatDateTime(d.created_at)}</dd>
          {d.transaction_id && (
            <>
              <dt className="text-slate-500">{t.transaction}</dt>
              <dd className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-xs break-all">{d.transaction_id}</span>
                <CopyButton text={d.transaction_id} label={t.transaction} />
              </dd>
            </>
          )}
          {d.resolved_at && (
            <>
              <dt className="text-slate-500">{t.resolvedAt}</dt>
              <dd>{formatDateTime(d.resolved_at)}</dd>
            </>
          )}
          {d.resolution_note && (
            <>
              <dt className="text-slate-500">{t.note}</dt>
              <dd className="whitespace-pre-wrap break-words" data-testid="resolution-note">
                {d.resolution_note}
              </dd>
            </>
          )}
        </dl>
        <h2 className="mt-4 mb-1 text-sm font-semibold text-slate-700">{t.details}</h2>
        {/* Rendered as a text node: nothing in details can become markup (AC-10). */}
        <pre data-testid="discrepancy-details" className="max-h-96 overflow-auto rounded-lg bg-slate-900 p-3 text-xs text-slate-100">
          {JSON.stringify(d.details, null, 2)}
        </pre>
      </div>
      {d.status === 'OPEN' && <ResolveForm id={d.id} onResolved={onResolved} onStale={onStale} onGone={onGone} />}
    </div>
  );
}

function ResolveForm({ id, onResolved, onStale, onGone }: { id: string } & Omit<ViewProps, 'd'>) {
  const { client } = useOps();
  const noteId = useId();
  const [note, setNote] = useState('');
  const [noteError, setNoteError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const noteRef = useRef<HTMLTextAreaElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);

  const resolve = useMutation({
    mutationKey: ['ops', 'resolve', id],
    mutationFn: async (text: string) =>
      unwrap(await client.POST('/internal/v1/discrepancies/{id}/resolve', { params: { path: { id } }, body: { note: text } })),
    onSuccess: (d) => onResolved(d),
    onError: (err) => {
      if (isApiError(err) && err.code === 'INVALID_NOTE') {
        setNoteError(opsMessage(err));
        return;
      }
      if (isApiError(err) && err.code === 'ALREADY_RESOLVED') return onStale();
      setError(opsMessage(err));
      if (isApiError(err) && err.code === 'DISCREPANCY_NOT_FOUND') onGone();
    },
    onSettled: () => setConfirming(false),
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!validateResolutionNote(note)) {
      setNoteError(t.noteInvalid);
      noteRef.current?.focus();
      return;
    }
    setNoteError(null);
    setError(null);
    setConfirming(true);
  };

  return (
    <form noValidate onSubmit={onSubmit} className="space-y-3 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm sm:p-5">
      <div ref={errorRef} tabIndex={-1} className="focus:outline-none">
        {error && <Alert tone="error">{error}</Alert>}
      </div>
      <div className="space-y-1">
        <label htmlFor={noteId} className="block text-sm font-medium text-slate-700">
          {t.note}
        </label>
        <textarea
          ref={noteRef}
          id={noteId}
          rows={3}
          value={note}
          onChange={(e) => {
            setNote(e.target.value);
            setNoteError(null);
          }}
          aria-invalid={noteError ? true : undefined}
          aria-describedby={`${noteId}-hint${noteError ? ` ${noteId}-error` : ''}`}
          className={`block w-full rounded-lg border bg-white px-3 py-2 text-base shadow-sm focus:outline-none focus:ring-2 focus:ring-brand-600 ${
            noteError ? 'border-red-500' : 'border-slate-300'
          }`}
        />
        <p id={`${noteId}-hint`} className="text-xs text-slate-500">
          {t.noteHint}
        </p>
        <div aria-live="polite">
          {noteError && (
            <p id={`${noteId}-error`} className="text-xs text-red-700">
              {noteError}
            </p>
          )}
        </div>
      </div>
      <Button className="sm:w-auto">{t.resolve}</Button>
      {confirming && (
        <ConfirmDialog
          title={t.confirmTitle}
          confirmLabel={t.resolve}
          busy={resolve.isPending}
          onCancel={() => setConfirming(false)}
          returnFocus={() => (errorRef.current?.childElementCount ? errorRef.current : noteRef.current)}
          onConfirm={() => resolve.mutate(note.trim())}
        >
          <p>{t.confirmBody}</p>
        </ConfirmDialog>
      )}
    </form>
  );
}
