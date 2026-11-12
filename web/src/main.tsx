import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createBrowserRouter } from 'react-router';
import { createApiClient } from '@/api/client';
import { App } from '@/app/App';
import { routes } from '@/app/routes';
import { Session, browserSessionDeps } from '@/auth/session';
import './index.css';

const session = new Session(browserSessionDeps());
const api = createApiClient(session, (input) => globalThis.fetch(input), window.location.origin);
const router = createBrowserRouter(routes);

// Restore a stored session before the first protected render (TDR-020 §2a).
void session.restore();

const root = document.getElementById('root');
if (!root) throw new Error('#root missing');
createRoot(root).render(
  <StrictMode>
    <App session={session} api={api} router={router} />
  </StrictMode>,
);
