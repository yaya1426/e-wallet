# TDR-020 — Browser Session Handling (Tokens, Refresh, Tabs, CSP)

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-12, UI planning) |
| Date       | 2026-11-12 |
| Deciders   | Yahya Elaraby |
| Related    | PRD-005 UI-BR-11/12, TDR-011, TDR-018 §3, TDR-019 |

## Context
The API returns a 15-minute access token and a 30-day **rotating** refresh token in the JSON
body (TDR-018 §3). Reusing an already-used refresh token revokes the whole session (reuse
detection). A browser app with several tabs, and several requests in flight, can easily send the
same refresh token twice and sign the user out. The backend does not set cookies.

## Decision
### 1. Storage
- **Access token:** in memory only (JS module + React context), with its expiry time
  (`now + expires_in`). Lost on reload; recovered with the refresh token.
- **Refresh token:** `localStorage` key `ewallet.session.v1` holding `{refresh_token, refresh_expires_at}`.
- Nothing else about the user is stored.

### 2. Refresh
- **Single refresh at a time per browser.** Every refresh runs inside
  `navigator.locks.request('ewallet-refresh', …)` (Web Locks API). Inside the lock the code
  **re-reads** the refresh token from `localStorage` (another tab may have rotated it), calls
  `POST /v1/auth/refresh`, and writes the new token before releasing the lock. In the same tab,
  concurrent callers share one in-flight promise.
- **When:** (a) on app start if a stored, unexpired refresh token exists; (b) proactively 60 s
  before the access token expires; (c) reactively when an API call returns `401
  UNAUTHENTICATED` — refresh once, then retry the original request once. A second 401 ends the session.
- **Failure:** `401 INVALID_REFRESH_TOKEN` or `403 USER_DISABLED` → clear storage and memory,
  go to login with the PRD-005 message. A network error does not clear the session; the request
  fails and the user can retry.

### 3. Logout and other tabs
- Logout calls `POST /v1/auth/logout` with the access and refresh tokens, then clears memory
  and storage **whatever the result** (the API is idempotent).
- Each tab listens to the `storage` event: when `ewallet.session.v1` is removed, it clears its
  in-memory token and goes to login. When it changes, nothing happens (the tab picks up the new
  token on its next refresh).

### 4. Content-Security-Policy
The built `index.html` (`web/dist/index.html`, what is deployed) has a meta CSP, injected at build
time from `web/csp.ts` by a Vite plugin and checked by `web/scripts/check-dist.ts`. The Vite dev server
runs without it because hot reload needs inline code; the e2e tests run against the built app.
The policy:
`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'`.
No inline scripts, no third-party origins, no `dangerouslySetInnerHTML`. (`frame-ancestors`
can't be set by meta; clickjacking protection waits for a header-capable host — see Revisit.)

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| Refresh token in an httpOnly cookie | Not readable by XSS | Needs backend changes (cookie, CSRF) and a new backend task | User chose no backend change now |
| Both tokens in memory only | Nothing persisted | Logged out on every reload; breaks UI-BR-11 | Bad UX |
| `sessionStorage` | Per tab | Each tab logs in separately; lost on browser restart | Breaks UI-BR-11 |
| BroadcastChannel leader tab | Fewer refreshes | More code; Web Locks already solves ordering | Web Locks is simpler |

## Consequences
- Positive: no backend change; no accidental reuse-detection logouts; consistent sign-out across tabs.
- Negative / trade-offs: an XSS bug could read the refresh token; mitigated by the strict CSP, React escaping and no third-party scripts.
- Follow-ups: move the refresh token to an httpOnly cookie when the backend adds cookie support.

## Revisit When
- Any third-party script must be loaded, or a security review asks for httpOnly cookies.
- The UI moves to a host that can set response headers (add `frame-ancestors 'none'` and HSTS).
