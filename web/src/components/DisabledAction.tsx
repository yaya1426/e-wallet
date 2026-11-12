import { useId } from 'react';
import { en } from '@/i18n/en';

/**
 * A money action that is unavailable because the wallet is frozen (PRD-005 UI-BR-14). It stays
 * in the tab order with aria-disabled so keyboard and screen-reader users hear why.
 */
export function DisabledAction({ label, className }: { label: string; className: string }) {
  const reasonId = useId();
  return (
    <>
      <button type="button" aria-disabled="true" aria-describedby={reasonId} className={className} onClick={(e) => e.preventDefault()}>
        {label}
      </button>
      <span id={reasonId} className="sr-only">
        {en.home.frozenAction}
      </span>
    </>
  );
}
