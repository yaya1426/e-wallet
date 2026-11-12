// Link or replace the user's bank account (PRD-005 UI-BR-20, T032).
import { useRef, useState, type FormEvent } from 'react';
import { isApiError } from '@/api/errors';
import { Alert } from '@/components/Alert';
import { Card, Page } from '@/components/AppShell';
import { Button } from '@/components/Button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { ErrorWithRetry } from '@/components/ErrorWithRetry';
import { Spinner } from '@/components/Spinner';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { messageForError } from '@/lib/errorMessages';
import { formatIban, isValidEgyptianIban } from '@/lib/iban';
import { validateFullName } from '@/lib/validation';
import { useBankAccount, useLinkBankAccount } from './hooks';

export function BankAccountPage() {
  const account = useBankAccount();
  const [editing, setEditing] = useState(false);
  const [saved, setSaved] = useState(false);

  return (
    <Page title={en.bank.title}>
      <Card>
        {account.isPending && <Spinner small />}
        {account.isError && <ErrorWithRetry error={account.error} onRetry={() => void account.refetch()} />}
        {account.isSuccess && (
          <div className="space-y-4">
            {saved && <Alert tone="success">{en.bank.saved}</Alert>}
            {account.data ? (
              <dl className="space-y-1" data-testid="bank-account">
                <dt className="text-sm text-slate-500">{en.bank.iban}</dt>
                <dd className="font-mono text-base break-all">{formatIban(account.data.iban)}</dd>
                <dt className="pt-2 text-sm text-slate-500">{en.bank.holder}</dt>
                <dd>{account.data.holder_name}</dd>
                <dd className="pt-2 text-xs text-slate-500">{en.bank.updated(formatDateTime(account.data.updated_at))}</dd>
              </dl>
            ) : (
              <p className="text-slate-600">{en.bank.none}</p>
            )}
            {editing ? (
              <BankAccountForm
                replacing={account.data !== null}
                onDone={() => {
                  setEditing(false);
                  setSaved(true);
                }}
                onCancel={() => setEditing(false)}
              />
            ) : (
              <Button
                type="button"
                variant={account.data ? 'secondary' : 'primary'}
                onClick={() => {
                  setSaved(false);
                  setEditing(true);
                }}
              >
                {account.data ? en.bank.replace : en.bank.link}
              </Button>
            )}
          </div>
        )}
      </Card>
    </Page>
  );
}

interface FormProps {
  replacing: boolean;
  onDone: () => void;
  onCancel: () => void;
}

function BankAccountForm({ replacing, onDone, onCancel }: FormProps) {
  const link = useLinkBankAccount();
  const [iban, setIban] = useState('');
  const [holder, setHolder] = useState('');
  const [errors, setErrors] = useState<{ iban?: string; holder?: string }>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const ibanRef = useRef<HTMLInputElement>(null);
  const holderRef = useRef<HTMLInputElement>(null);

  const save = () => {
    setConfirming(false);
    setFormError(null);
    link.mutate(
      { iban, holder_name: holder },
      {
        onSuccess: onDone,
        onError: (err) => {
          if (isApiError(err) && err.code === 'INVALID_IBAN') {
            setErrors({ iban: en.bank.ibanInvalid });
            ibanRef.current?.focus();
          } else if (isApiError(err) && err.code === 'INVALID_HOLDER_NAME') {
            setErrors({ holder: en.bank.holderInvalid });
            holderRef.current?.focus();
          } else {
            setFormError(messageForError(err));
          }
        },
      },
    );
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    // Order mirrors the API: IBAN, then holder name; all errors are shown at once.
    const next = {
      iban: isValidEgyptianIban(iban) ? undefined : en.bank.ibanInvalid,
      holder: validateFullName(holder) ? en.bank.holderInvalid : undefined,
    };
    setErrors(next);
    if (next.iban) return ibanRef.current?.focus();
    if (next.holder) return holderRef.current?.focus();
    if (replacing) setConfirming(true);
    else save();
  };

  return (
    <form noValidate onSubmit={onSubmit} className="space-y-4 border-t border-slate-100 pt-4">
      {formError && <Alert tone="error">{formError}</Alert>}
      <TextField
        ref={ibanRef}
        label={en.bank.iban}
        hint={en.bank.ibanHint}
        value={iban}
        onChange={(e) => setIban(e.target.value)}
        error={errors.iban}
        autoComplete="off"
        autoCapitalize="characters"
        spellCheck={false}
        className="font-mono"
      />
      <TextField
        ref={holderRef}
        label={en.bank.holder}
        value={holder}
        onChange={(e) => setHolder(e.target.value)}
        error={errors.holder}
        autoComplete="name"
      />
      <div className="flex flex-col-reverse gap-2 sm:flex-row">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={link.isPending}>
          {en.bank.cancel}
        </Button>
        <Button type="submit" loading={link.isPending}>
          {en.bank.save}
        </Button>
      </div>
      {confirming && (
        <ConfirmDialog title={en.bank.replaceTitle} onConfirm={save} onCancel={() => setConfirming(false)}>
          <p>{en.bank.replaceBody}</p>
        </ConfirmDialog>
      )}
    </form>
  );
}
