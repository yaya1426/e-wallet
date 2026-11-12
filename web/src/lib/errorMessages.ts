import { ApiError, isApiError } from '@/api/errors';
import { en } from '@/i18n/en';

/** User-facing message for an API error (PRD-005 UI-BR-4). Never shows the API's own text. */
export function messageForError(err: unknown): string {
  if (isApiError(err)) {
    const known = en.errors[err.code];
    if (known) return known;
    return withReference(en.errors.GENERIC ?? '', err);
  }
  return en.errors.GENERIC ?? '';
}

function withReference(message: string, err: ApiError): string {
  return err.requestId ? `${message} (${en.common.reference}: ${err.requestId})` : message;
}
