# TDR-022 — Operator Console: Holding the Operator Key in the Browser

| Field      | Value |
|------------|-------|
| Status     | Accepted (2026-11-12, UI planning) |
| Date       | 2026-11-12 |
| Deciders   | Yahya Elaraby (PM mandate: recommended option taken, see T034 Decisions Log) |
| Related    | PRD-005 UI-BR-31..35, TDR-018 §5, TDR-019, TDR-020 |

## Context
The operator console (PRD-005 area 5) calls `/internal/v1/*`, which is protected only by the
static header `X-Internal-Api-Key` (TDR-018 §5). There is no operator login, no roles and no
cookie support in the backend, and PRD-005 says the backend does not change. The browser must
therefore hold the key while an operator works. The key is more powerful than any user token: it
can freeze wallets and issue password-reset codes for any user.

## Decision
1. **Entry.** The operator types or pastes the key on the `/ops` unlock screen. The UI checks it
   with `GET /internal/v1/health/ops` before accepting it.
2. **Storage.** The key is kept in `sessionStorage` under `ewallet.ops.v1` (this tab only; gone
   when the tab closes) and in memory. Never in `localStorage`, cookies, the URL, logs or error
   reports.
3. **Idle lock.** After 15 minutes without an operator action (any `/internal` call made by the
   operator or any click/keypress in the console), the key is removed and the unlock screen shown.
   Auto-refresh calls do not count as activity.
4. **Separate client.** A dedicated `opsClient` (openapi-fetch, same generated schema) adds
   `X-Internal-Api-Key` to `/internal/v1` requests only. It never sends the user's bearer token,
   and the user `client` never sends the key. Any `401` from `opsClient` clears the key.
5. **Code splitting.** The console is a lazily loaded route chunk (`React.lazy`) so the user app
   bundle does not contain operator code paths.
6. **Lock.** A "Lock" button in the console header removes the key at once.
7. The CSP from TDR-020 §4 applies unchanged (no third-party scripts).

## Alternatives Considered
| Option | Pros | Cons | Why not chosen |
|--------|------|------|----------------|
| Memory only | Nothing persisted | Key re-typed on every reload | Poor UX for long incident sessions; sessionStorage dies with the tab anyway |
| `localStorage` | Survives restarts | Key stays on the machine indefinitely, shared by all tabs | Too long-lived for a master key |
| Operator login in the backend (users with roles, short tokens) | Per-person audit, revocable | New backend feature, migrations, PRD | Out of PRD-005 scope; listed as a follow-up |
| Separate ops domain/app | Isolation from user app | Second deployable, CORS | More moving parts for one operator |

## Consequences
- Positive: no backend change; key lifetime bounded by the tab and a 15-minute idle timeout;
  user and operator credentials never mix.
- Negative / trade-offs: an XSS bug on the same origin while an operator tab is open could read
  the key; mitigated by the strict CSP and React escaping. All operators share one key, so actions
  are not attributed to a person (TDR-018 already accepts this).
- Follow-ups: per-operator accounts and roles in the backend; rotate `INTERNAL_API_KEY` if a
  shared machine was used.

## Revisit When
- More than one operator needs individual audit trails, or a security review asks for SSO.
- Any third-party script is added to the web app.
