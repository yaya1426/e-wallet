// Typed view of the API error body (TDR-013): {"error": {"code", "message", "request_id"}}.
export const NETWORK_ERROR = 'NETWORK_ERROR';
export const UNKNOWN_ERROR = 'UNKNOWN';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string | undefined;

  constructor(status: number, code: string, requestId?: string) {
    super(`API error ${status} ${code}`);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

/** Builds an ApiError from a parsed error body (or anything else the server sent). */
export function apiErrorFrom(status: number, body: unknown, headerRequestId?: string | null): ApiError {
  if (typeof body === 'object' && body !== null && 'error' in body) {
    const err = (body as { error: unknown }).error;
    if (typeof err === 'object' && err !== null && typeof (err as { code?: unknown }).code === 'string') {
      const { code, request_id } = err as { code: string; request_id?: unknown };
      return new ApiError(status, code, typeof request_id === 'string' ? request_id : (headerRequestId ?? undefined));
    }
  }
  return new ApiError(status, UNKNOWN_ERROR, headerRequestId ?? undefined);
}

export function networkError(): ApiError {
  return new ApiError(0, NETWORK_ERROR);
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}
