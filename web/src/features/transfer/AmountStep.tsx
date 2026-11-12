// Step 2: amount and optional note (PRD-005 UI-BR-28).
import { useId, type FormEvent, type Ref } from 'react';
import { AmountField } from '@/components/AmountField';
import { Button } from '@/components/Button';
import { en } from '@/i18n/en';
import { formatMoney } from '@/lib/money';
import { NOTE_MAX, noteLength } from '@/lib/validation';

interface Props {
  amount: string;
  onAmountChange: (value: string) => void;
  amountError: string | null;
  note: string;
  onNoteChange: (value: string) => void;
  noteError: string | null;
  availableMinor: number;
  onBack: () => void;
  onContinue: () => void;
  amountRef: Ref<HTMLInputElement>;
  noteRef: Ref<HTMLTextAreaElement>;
}

export function AmountStep({ amount, onAmountChange, amountError, note, onNoteChange, noteError, availableMinor, onBack, onContinue, amountRef, noteRef }: Props) {
  const noteId = useId();
  const counterId = `${noteId}-counter`;
  const errorId = `${noteId}-error`;
  const used = noteLength(note);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onContinue();
  };
  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <AmountField ref={amountRef} value={amount} onChange={(e) => onAmountChange(e.target.value)} error={amountError} />
      <p className="text-sm text-slate-600">{en.amount.available(formatMoney(availableMinor))}</p>
      <div className="space-y-1">
        <label htmlFor={noteId} className="block text-sm font-medium text-slate-700">
          {en.transfer.note}
        </label>
        <textarea
          ref={noteRef}
          id={noteId}
          rows={2}
          value={note}
          onChange={(e) => onNoteChange(e.target.value)}
          aria-invalid={noteError ? true : undefined}
          aria-describedby={[counterId, noteError ? errorId : null].filter(Boolean).join(' ')}
          className={`block w-full rounded-lg border bg-white px-3 py-2 text-base shadow-sm focus:outline-none focus:ring-2 focus:ring-brand-600 ${
            noteError ? 'border-red-500' : 'border-slate-300'
          }`}
        />
        <p id={counterId} className={`text-right text-xs tabular-nums ${used > NOTE_MAX ? 'text-red-700' : 'text-slate-500'}`}>
          {en.transfer.noteCounter(used)}
        </p>
        <div aria-live="polite">
          {noteError && (
            <p id={errorId} className="text-xs text-red-700">
              {noteError}
            </p>
          )}
        </div>
      </div>
      <div className="flex flex-col-reverse gap-2 sm:flex-row">
        <Button type="button" variant="secondary" onClick={onBack}>
          {en.transfer.back}
        </Button>
        <Button>{en.transfer.continue}</Button>
      </div>
    </form>
  );
}
