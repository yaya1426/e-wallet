import { isApiError } from '@/api/errors';
import { Alert } from '@/components/Alert';
import { Button } from '@/components/Button';
import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';

/** Operator wording for an /internal API error; falls back to the shared messages. */
export function opsMessage(err: unknown): string {
  if (isApiError(err)) {
    const known = en.opsErrors[err.code];
    if (known) return known;
  }
  return messageForError(err);
}

export function OpsErrorWithRetry({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div className="space-y-2">
      <Alert tone="error">{opsMessage(error)}</Alert>
      <Button type="button" variant="secondary" className="sm:w-auto" onClick={onRetry}>
        {en.common.retry}
      </Button>
    </div>
  );
}
