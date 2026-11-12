// Typed API client (TDR-019 §1): openapi-fetch over the generated schema, with a fetch wrapper
// that adds the access token and recovers from an expired one (TDR-020 §2c).
import createClient from 'openapi-fetch';
import type { Session } from '@/auth/session';
import { ApiError, apiErrorFrom, networkError } from './errors';
import type { paths } from './schema';

/** Paths that never carry the user's access token. */
const publicPaths = new Set(['/v1/auth/login', '/v1/auth/register', '/v1/auth/refresh', '/v1/auth/password-reset']);

type Fetch = (input: Request) => Promise<Response>;

export function createAuthFetch(session: Session, baseFetch: Fetch): Fetch {
  const send = async (req: Request): Promise<Response> => {
    try {
      return await baseFetch(req);
    } catch {
      throw networkError();
    }
  };

  return async (req) => {
    const path = new URL(req.url).pathname;
    if (publicPaths.has(path) || path.startsWith('/internal/')) return send(req);

    const backup = req.clone();
    let token = session.getAccessToken();
    if (!token && session.getState().status !== 'anonymous') {
      // Expired (e.g. the tab slept through the proactive refresh). A failure here is the
      // answer: sending without a token would only turn a network error into "session ended".
      token = await session.refresh();
    }
    let res = await send(withBearer(req, token));

    // Only a request that carried a token is worth a refresh; an anonymous caller just gets
    // its 401 back.
    if (res.status === 401 && token && (await codeOf(res)) === 'UNAUTHENTICATED') {
      let fresh = session.getAccessToken();
      if (!fresh || fresh === token) {
        // Nobody refreshed since this request was sent: do it now (shared with other callers).
        try {
          fresh = await session.refresh();
        } catch {
          return res; // refresh() already ended the session when it was terminal
        }
      }
      res = await send(withBearer(backup, fresh));
      if (res.status === 401) {
        session.end('sessionEnded');
        return res;
      }
    }
    if (res.status === 403 && (await codeOf(res)) === 'USER_DISABLED') {
      session.end('userDisabled');
    }
    return res;
  };
}

function withBearer(req: Request, token: string | null): Request {
  if (!token) return req;
  const headers = new Headers(req.headers);
  headers.set('Authorization', `Bearer ${token}`);
  return new Request(req, { headers });
}

async function codeOf(res: Response): Promise<string | null> {
  try {
    const body: unknown = await res.clone().json();
    return apiErrorFrom(res.status, body).code;
  } catch {
    return null;
  }
}

export function createApiClient(session: Session, baseFetch: Fetch, origin: string) {
  return createClient<paths>({ baseUrl: origin, fetch: createAuthFetch(session, baseFetch) });
}

export type ApiClient = ReturnType<typeof createApiClient>;

/**
 * Turns an openapi-fetch result into data or a thrown ApiError, so TanStack Query sees errors.
 * Use it as `unwrap(await api.GET('/v1/me/wallet'))`.
 */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.response.ok) return result.data as T;
  throw apiErrorFrom(result.response.status, result.error, result.response.headers.get('X-Request-ID'));
}

export { ApiError };
