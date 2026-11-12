// TDR-022 §4: the operator console's own client. It adds X-Internal-Api-Key to /internal/v1
// calls only, never sends the user's bearer token, and a 401 drops the key.
import createClient from 'openapi-fetch';
import type { OpsKeyStore } from '@/features/ops/opsKey';
import { networkError } from './errors';
import type { paths } from './schema';

type Fetch = (input: Request) => Promise<Response>;

export const OPS_KEY_HEADER = 'X-Internal-Api-Key';

export function createOpsFetch(store: OpsKeyStore, baseFetch: Fetch): Fetch {
  return async (req) => {
    const path = new URL(req.url).pathname;
    const headers = new Headers(req.headers);
    headers.delete('Authorization');
    // The unlock check sends its candidate key itself; everything else uses the stored one.
    const candidate = headers.has(OPS_KEY_HEADER);
    if (!candidate) {
      const key = store.get();
      if (key && path.startsWith('/internal/')) headers.set(OPS_KEY_HEADER, key);
    }
    // An operator action (not a background read) counts as activity (TDR-022 §3).
    if (req.method !== 'GET') store.touch();
    let res: Response;
    try {
      res = await baseFetch(new Request(req, { headers }));
    } catch {
      throw networkError();
    }
    if (res.status === 401 && !candidate) store.clear('rejected');
    return res;
  };
}

export function createOpsClient(store: OpsKeyStore, baseFetch: Fetch, origin: string) {
  return createClient<paths>({ baseUrl: origin, fetch: createOpsFetch(store, baseFetch) });
}

export type OpsClient = ReturnType<typeof createOpsClient>;
