import { forwardRef, useState } from 'react';
import { en } from '@/i18n/en';
import { TextField, type TextFieldProps } from './TextField';

export const PasswordField = forwardRef<HTMLInputElement, Omit<TextFieldProps, 'type'>>(function PasswordField(
  props,
  ref,
) {
  const [visible, setVisible] = useState(false);
  return (
    <TextField ref={ref} {...props} type={visible ? 'text' : 'password'} className="pr-16">
      <button
        type="button"
        onClick={() => setVisible((v) => !v)}
        aria-pressed={visible}
        aria-label={visible ? en.common.hidePassword : en.common.showPassword}
        className="absolute inset-y-0 right-0 px-3 text-xs font-medium text-slate-600 hover:text-slate-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600"
      >
        {visible ? en.common.hide : en.common.show}
      </button>
    </TextField>
  );
});
