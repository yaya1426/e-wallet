// UI-BR-33: freeze / unfreeze a wallet by ID, with a confirmation.
import { useMutation } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import { unwrap } from '@/api/client';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { isUuid } from '@/lib/validation';
import { useOps } from './context';
import { OpsPage } from './OpsLayout';
import { opsMessage } from './opsErrors';

const t = en.ops.wallets;
type Action = 'freeze' | 'unfreeze';

export function WalletsPage() {
  const { client } = useOps();
  const [walletId, setWalletId] = useState('');
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<{ action: Action; id: string } | null>(null);
  const [result, setResult] = useState<{ tone: 'success' | 'error'; text: string } | null>(null);
  const field = useRef<HTMLInputElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);

  const change = useMutation({
    mutationKey: ['ops', 'walletStatus'],
    gcTime: 0,
    mutationFn: async ({ action, id }: { action: Action; id: string }) => {
      const path = { params: { path: { wallet_id: id } } };
      return unwrap(
        action === 'freeze'
          ? await client.POST('/internal/v1/wallets/{wallet_id}/freeze', path)
          : await client.POST('/internal/v1/wallets/{wallet_id}/unfreeze', path),
      );
    },
    onSuccess: (w) => setResult({ tone: 'success', text: t.result(w.wallet_id, w.status) }),
    onError: (err) => setResult({ tone: 'error', text: opsMessage(err) }),
    onSettled: () => setConfirming(null),
  });

  const ask = (action: Action) => {
    const id = walletId.trim().toLowerCase();
    if (!isUuid(id)) {
      setFieldError(t.invalidId);
      field.current?.focus();
      return;
    }
    setFieldError(null);
    setResult(null);
    setConfirming({ action, id });
  };

  return (
    <OpsPage title={t.title}>
      <div className="space-y-4 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm sm:p-5">
        <p className="text-sm text-slate-600">{t.intro}</p>
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            ask('freeze');
          }}
          className="space-y-4"
        >
          <TextField
            ref={field}
            label={t.walletId}
            hint={t.walletIdHint}
            name="wallet-id"
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
            value={walletId}
            onChange={(e) => {
              setWalletId(e.target.value);
              setFieldError(null);
            }}
            error={fieldError}
          />
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button variant="danger" className="sm:w-auto">
              {t.freeze}
            </Button>
            <Button type="button" variant="secondary" className="sm:w-auto" onClick={() => ask('unfreeze')}>
              {t.unfreeze}
            </Button>
          </div>
        </form>
        <div ref={resultRef} tabIndex={-1} className="focus:outline-none">
          {result && <Alert tone={result.tone}>{result.text}</Alert>}
        </div>
      </div>
      {confirming && (
        <ConfirmDialog
          title={confirming.action === 'freeze' ? t.freezeTitle : t.unfreezeTitle}
          confirmLabel={confirming.action === 'freeze' ? t.freeze : t.unfreeze}
          busy={change.isPending}
          onCancel={() => setConfirming(null)}
          returnFocus={() => (resultRef.current?.childElementCount ? resultRef.current : field.current)}
          onConfirm={() => change.mutate(confirming)}
        >
          <p className="break-all">{confirming.action === 'freeze' ? t.freezeBody(confirming.id) : t.unfreezeBody(confirming.id)}</p>
        </ConfirmDialog>
      )}
    </OpsPage>
  );
}
