import { render } from '@testing-library/react';
import { createMemoryRouter } from 'react-router';
import { createApiClient } from '@/api/client';
import { App, createQueryClient } from '@/app/App';
import { routes } from '@/app/routes';
import { Session } from '@/auth/session';
import { fakeEnv, FakeServer, json, ORIGIN, tokens } from './fakes';

interface Options {
  server?: FakeServer;
  signedIn?: boolean;
  /** Called before the first render (e.g. to seed storage). */
  before?: (env: ReturnType<typeof fakeEnv>) => void;
}

/** Renders the whole app at `path` with a fake server, like a real page load. */
export async function renderApp(path: string, { server = new FakeServer(), signedIn = false, before }: Options = {}) {
  const env = fakeEnv(server);
  const session = new Session(env.deps());
  if (signedIn) {
    server.on('POST /v1/auth/login', () => json(200, tokens(1)));
    await session.login('+201001234567', 'secret123');
  } else {
    before?.(env);
    await session.restore();
  }
  const api = createApiClient(session, server.fetch, ORIGIN);
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const queryClient = createQueryClient();
  const utils = render(<App session={session} api={api} router={router} queryClient={queryClient} />);
  return { ...utils, env, session, server, router, queryClient };
}
