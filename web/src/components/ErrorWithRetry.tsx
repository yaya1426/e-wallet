import { en } from '@/i18n/en';
import { messageForError } from '@/lib/errorMessages';
import { Alert } from './Alert';
import { Button } from './Button';

/** A failed load: the mapped message (UI-BR-4) and a Retry button. */
export function ErrorWithRetry({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <div className="mt-2 space-y-2">
      <Alert tone="error">{messageForError(error)}</Alert>
      <Button type="button" variant="secondary" onClick={onRetry}>
        {en.common.retry}
      </Button>
    </div>
  );
}
