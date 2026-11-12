import { forwardRef, useId, type InputHTMLAttributes } from 'react';

export interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'id'> {
  label: string;
  hint?: string;
  error?: string | null;
}

/** A labelled input whose hint and error are linked with aria-describedby (PRD-005 UI-NFR-2). */
export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { label, hint, error, className = '', children, ...rest },
  ref,
) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : null, error ? errorId : null].filter(Boolean).join(' ') || undefined;
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-slate-700">
        {label}
      </label>
      <div className="relative">
        <input
          ref={ref}
          id={id}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={`block w-full rounded-lg border bg-white px-3 py-2 text-base shadow-sm focus:outline-none focus:ring-2 focus:ring-brand-600 ${
            error ? 'border-red-500' : 'border-slate-300'
          } ${className}`}
          {...rest}
        />
        {children}
      </div>
      {hint && !error && (
        <p id={hintId} className="text-xs text-slate-500">
          {hint}
        </p>
      )}
      {/* Always rendered so screen readers announce an error when it appears (UI-NFR-2). */}
      <div aria-live="polite">
        {error && (
          <p id={errorId} className="text-xs text-red-700">
            {error}
          </p>
        )}
      </div>
    </div>
  );
});
