// UI-BR-35: issue a one-time password reset code. The code lives only in this component.
import { useMutation } from '@tanstack/react-query';
import { useRef, useState, type FormEvent } from 'react';
import { unwrap } from '@/api/client';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { ConfirmDialog } from '@/components/ConfirmDialog';
import { CopyButton } from '@/components/CopyButton';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { formatDateTime } from '@/lib/datetime';
import { formatPhone, normalizeEgyptianMobile } from '@/lib/phone';
import { useOps } from './context';
import { OpsPage } from './OpsLayout';
import { opsMessage } from './opsErrors';

const t = en.ops.reset;

export function ResetCodePage() {
  const { client } = useOps();
  const [phone, setPhone] = useState('');
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const field = useRef<HTMLInputElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);

  const issue = useMutation({
    mutationKey: ['ops', 'resetCode'],
    gcTime: 0, // the code must not outlive this screen (UI-BR-35)
    // openapi-fetch escapes the path parameter, so "+" goes out as %2B.
    mutationFn: async (e164: string) =>
      unwrap(await client.POST('/internal/v1/users/{phone}/password-reset-code', { params: { path: { phone: e164 } } })),
    onSuccess: () => setError(null),
    onError: (err) => setError(opsMessage(err)),
    onSettled: () => setConfirming(null),
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    const e164 = normalizeEgyptianMobile(phone);
    if (!e164) {
      setFieldError(en.validation.phoneInvalid);
      field.current?.focus();
      return;
    }
    setFieldError(null);
    setError(null);
    issue.reset();
    setConfirming(e164);
  };

  const code = issue.data;
  return (
    <OpsPage title={t.title}>
      <div className="space-y-4 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm sm:p-5">
        <p className="text-sm text-slate-600">{t.intro}</p>
        <form noValidate onSubmit={onSubmit} className="space-y-4">
          <TextField
            ref={field}
            label={t.phone}
            hint={en.fields.phoneHint}
            name="user-phone"
            type="tel"
            inputMode="tel"
            autoComplete="off"
            value={phone}
            onChange={(e) => {
              setPhone(e.target.value);
              setFieldError(null);
            }}
            error={fieldError}
          />
          <Button className="sm:w-auto">{t.issue}</Button>
        </form>
        <div ref={resultRef} tabIndex={-1} className="focus:outline-none">
          {error && <Alert tone="error">{error}</Alert>}
          {code && (
            <div className="space-y-2 rounded-xl border border-slate-200 bg-slate-50 p-4" data-testid="reset-code">
              <p className="text-sm text-slate-600">{t.codeFor(formatPhone(code.phone))}</p>
              <div className="flex flex-wrap items-center gap-3">
                <p className="font-mono text-4xl font-bold tracking-[0.3em] text-slate-900">
                  <span aria-hidden="true" data-testid="reset-code-value">
                    {code.code}
                  </span>
                  {/* Read digit by digit, not as a number. */}
                  <span className="sr-only">{t.codeLabel(code.code.split('').join(' '))}</span>
                </p>
                <CopyButton text={code.code} label={t.copyLabel} />
              </div>
              <p className="text-sm text-slate-600">{t.expires(formatDateTime(code.expires_at))}</p>
            </div>
          )}
        </div>
      </div>
      {confirming && (
        <ConfirmDialog
          title={t.confirmTitle}
          confirmLabel={t.issue}
          busy={issue.isPending}
          onCancel={() => setConfirming(null)}
          returnFocus={() => (resultRef.current?.childElementCount ? resultRef.current : field.current)}
          onConfirm={() => issue.mutate(confirming)}
        >
          <p>{t.confirmBody(formatPhone(confirming))}</p>
        </ConfirmDialog>
      )}
    </OpsPage>
  );
}
