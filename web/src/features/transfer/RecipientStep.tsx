// Step 1: who to send to, with the masked-name preview (PRD-005 UI-BR-27).
import type { FormEvent, Ref } from 'react';
import { Button } from '@/components/Button';
import { TextField } from '@/components/TextField';
import { en } from '@/i18n/en';
import { formatPhone } from '@/lib/phone';
import type { Recipient } from './hooks';

interface Props {
  phone: string;
  onPhoneChange: (value: string) => void;
  error: string | null;
  recipient: Recipient | null;
  checking: boolean;
  onCheck: () => void;
  onChange: () => void;
  onContinue: () => void;
  phoneRef: Ref<HTMLInputElement>;
  previewRef: Ref<HTMLDivElement>;
}

export function RecipientStep({ phone, onPhoneChange, error, recipient, checking, onCheck, onChange, onContinue, phoneRef, previewRef }: Props) {
  if (recipient) {
    return (
      <div className="space-y-4">
        <div
          ref={previewRef}
          tabIndex={-1}
          className="rounded-xl border border-slate-200 bg-slate-50 p-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600"
          data-testid="recipient-preview"
        >
          <p className="text-lg font-semibold">{en.transfer.sendTo(recipient.masked_name)}</p>
          <p className="text-sm text-slate-600 tabular-nums">{formatPhone(recipient.phone)}</p>
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row">
          <Button type="button" variant="secondary" onClick={onChange}>
            {en.transfer.change}
          </Button>
          <Button type="button" onClick={onContinue}>
            {en.transfer.continue}
          </Button>
        </div>
      </div>
    );
  }

  const submit = (e: FormEvent) => {
    e.preventDefault();
    onCheck();
  };
  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <TextField
        ref={phoneRef}
        label={en.transfer.phone}
        hint={en.fields.phoneHint}
        name="recipient"
        type="tel"
        inputMode="tel"
        autoComplete="off"
        value={phone}
        onChange={(e) => onPhoneChange(e.target.value)}
        error={error}
        readOnly={checking}
      />
      <Button loading={checking}>{en.transfer.continue}</Button>
    </form>
  );
}
