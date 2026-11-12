import { useState } from 'react';
import { messageForError } from './errorMessages';
import { isApiError, type ApiError } from '@/api/errors';

/**
 * Runs a form action, tracking `pending` and the form-level error message. `onApiError` may
 * claim an error for a field (return true) so it is not repeated at the top of the form.
 */
export function useSubmit() {
  const [pending, setPending] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  async function run(action: () => Promise<void>, onApiError?: (err: ApiError) => boolean): Promise<void> {
    setPending(true);
    setFormError(null);
    try {
      await action();
    } catch (err) {
      if (!(isApiError(err) && onApiError?.(err))) setFormError(messageForError(err));
    } finally {
      setPending(false);
    }
  }

  return { pending, formError, setFormError, run };
}
