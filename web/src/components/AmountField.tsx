import { forwardRef } from 'react';
import { en } from '@/i18n/en';
import { TextField, type TextFieldProps } from './TextField';

/** Amount input with an "EGP" prefix (PRD-005 UI-BR-21). Parsing is `parseAmount` (lib/money). */
export const AmountField = forwardRef<HTMLInputElement, Omit<TextFieldProps, 'label' | 'type' | 'inputMode'> & { label?: string }>(
  function AmountField({ label = en.amount.label, hint = en.amount.hint, ...rest }, ref) {
    return (
      <TextField
        ref={ref}
        label={label}
        hint={hint}
        type="text"
        inputMode="decimal"
        autoComplete="off"
        className="pl-12 tabular-nums"
        {...rest}
      >
        <span aria-hidden="true" className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-sm text-slate-500">
          EGP
        </span>
      </TextField>
    );
  },
);
