# PRD-001 — E-Wallet Core (Product Overview)

| Field        | Value                                   |
|--------------|-----------------------------------------|
| Status       | Proposed — awaiting review              |
| Owner        | Yahya Elaraby                           |
| Created      | 2026-11-03                              |
| Related PRDs | PRD-002 Top-up, PRD-003 Withdrawal, PRD-004 P2P Transfer |
| Related TDRs | TDR-001 … TDR-013                       |

---

## 1. Problem & Vision

We are building a **functional e-wallet** for Egyptian users. A user holds a balance in
**Egyptian Pounds (EGP)** inside our wallet and can:

1. **Top up** — move money from their bank account into the wallet.
2. **Withdraw** — move money from the wallet back to their bank account.
3. **Send peer-to-peer (P2P)** — move money from their wallet to another user's wallet.

The product's top priority is **correctness of money**: no money is ever created, lost or
duplicated, every balance can be explained line by line, and the system recovers on its own
from bank delays, network failures, duplicate requests and lost webhooks.

## 2. Goals

| # | Goal | How we measure it |
|---|------|-------------------|
| G1 | Money is always correct | Sum of all ledger postings is always 0; every wallet balance equals the sum of its postings; zero reconciliation breaks left unexplained. |
| G2 | No duplicate money movement | A repeated request (double click, client retry, network retry) results in exactly one movement. |
| G3 | Resilient to the bank | No top-up or withdrawal is lost if the bank is slow, down, or never sends a webhook. Every bank-bound operation reaches a final state (`COMPLETED` / `FAILED`) automatically. |
| G4 | Auditable | Every balance change is traceable to a transaction, a ledger entry, and (for bank flows) a bank reference. Ledger records are never updated or deleted. |
| G5 | Simple to run | One Go service + one PostgreSQL database (+ a bank simulator for development). |

## 3. Non-Goals (this phase)

- Multiple currencies or FX. **EGP only.**
- Real bank integration. The bank is **simulated** (see PRD-002/003 and TDR-008).
- KYC / identity verification, AML screening, regulatory reporting.
- Cards, merchants, QR payments, bill payments.
- Fees, cashback, interest.
- Mobile / web front-end. We deliver an HTTP JSON API only.
- Microservices or a message broker (Kafka, Pub/Sub, RabbitMQ). Possible future decision (TDR-006).
- Notifications (SMS / push / email).

## 4. Personas

| Persona | Description |
|---------|-------------|
| **Wallet user** | An individual with one EGP wallet who tops up, withdraws and sends money. |
| **Operator** (internal) | Ops/finance staff who look at failed transactions and reconciliation reports. In this phase the operator works through read-only endpoints and the database; no admin UI. |
| **External bank** (system) | The company's bank. Holds the company's settlement account, pulls money from users' bank accounts (top-up), pushes money to them (withdrawal), and notifies us by webhook. Simulated in this phase. |

## 5. Core Business Rules (apply to every flow)

### 5.1 Currency and amounts
- **BR-1** The only currency is **EGP**.
- **BR-2** All amounts are stored, computed and transferred as **integers in piasters**
  (قرش; 1 EGP = 100 piasters). Example: 150.75 EGP = `15075`. Floating point is forbidden
  everywhere: database, Go code, and API. (TDR-003)
- **BR-3** The API accepts and returns amounts as an **integer number of piasters** in a field
  named `amount_minor` together with `"currency": "EGP"`. The API never accepts decimals.
- **BR-4** Every movement amount must be **> 0**. Zero and negative amounts are rejected.

### 5.2 Wallet
- **BR-5** Each user has **exactly one wallet** in EGP, created automatically at registration.
- **BR-6** A wallet balance **can never go below zero**. There is no overdraft.
- **BR-7** The wallet balance is **derived from the ledger** (double-entry). The stored balance
  is a cached projection that is updated in the same database transaction as the ledger
  postings (TDR-004).
- **BR-8** A wallet has a status: `ACTIVE` or `FROZEN`. A frozen wallet cannot send, withdraw
  or top up; it can still receive P2P transfers and webhooks for already-started operations
  are still applied. (Freezing is done by an operator directly in this phase.)

### 5.3 Balances shown to the user
- **BR-9** The user sees one figure: **available balance**. Money reserved for an in-flight
  withdrawal is already removed from the available balance (see PRD-003).
- **BR-10** Money from a top-up appears only after the bank **confirms** it. A pending top-up
  is visible in transaction history but does not increase the balance.

### 5.4 Transactions & ledger
- **BR-11** Every user action creates one **transaction** record (business view: what the user
  asked for, its status, its bank reference).
- **BR-12** Every balance effect is recorded in the **ledger** as a balanced journal entry
  (sum of debits = sum of credits). The ledger is the **system of record**.
- **BR-13** Ledger entries are **append-only**. Mistakes are fixed by a new reversing entry,
  never by update or delete.
- **BR-14** Transaction statuses:

  | Status | Meaning |
  |--------|---------|
  | `PENDING` | Accepted and stored; not yet sent to the bank. |
  | `PROCESSING` | Sent to the bank; waiting for the final answer. |
  | `COMPLETED` | Final. Money moved. |
  | `FAILED` | Final. No money moved (or any reserved money was returned). |

  P2P transfers are synchronous and go straight to `COMPLETED` (or are rejected before a
  transaction is created — see PRD-004).
- **BR-15** `COMPLETED` and `FAILED` are **terminal**. A transaction never leaves a terminal
  state. A late or conflicting bank message for a terminal transaction is recorded and
  raised as a reconciliation discrepancy, never applied silently.

### 5.5 Idempotency
- **BR-16** Every money-moving request (`POST` top-up, withdrawal, transfer) **must** carry an
  `Idempotency-Key` header. Requests without it are rejected (`400`). (TDR-005)
- **BR-17** The same key + same user + same payload → the **original response** is returned
  and nothing new happens. Same key with a **different payload** → `422`. Same key while the
  first request is still being processed → `409`.
- **BR-18** Idempotency keys are kept for **24 hours** at least; transaction-level uniqueness
  (user + key) is kept **forever**.
- **BR-19** Calls from the wallet to the bank are also idempotent: the wallet sends its own
  transaction ID as the bank-side idempotency key, so retries never charge or pay twice.

### 5.6 Limits (proposed — must be confirmed)
All limits are **configuration**, not code. Proposed defaults:

| Limit | Proposed value |
|-------|----------------|
| Minimum top-up | 10.00 EGP (`1000`) |
| Maximum single top-up | 30,000.00 EGP (`3000000`) |
| Minimum withdrawal | 10.00 EGP (`1000`) |
| Maximum single withdrawal | 30,000.00 EGP (`3000000`) |
| Minimum P2P | 1.00 EGP (`100`) |
| Maximum single P2P | 30,000.00 EGP (`3000000`) |
| Maximum wallet balance | 100,000.00 EGP (`10000000`) |
| Daily outgoing (withdraw + P2P) per user | 60,000.00 EGP (`6000000`) |

> **Open question Q1:** confirm limits (real-world limits depend on the CBE regulation and the
> partner bank). Daily limits use the **Africa/Cairo** calendar day.

### 5.7 Fees
- **BR-20** No fees in this phase. The ledger design must allow adding a fee account later
  without changing existing entries.

## 6. Functional Scope

| Feature | PRD | Sync / Async |
|---------|-----|--------------|
| User registration & login (minimal) | this PRD §7 | sync |
| Get wallet & balance | this PRD §7 | sync |
| Transaction history | this PRD §7 | sync |
| Top-up from bank | PRD-002 | async (outbox + webhook) |
| Withdrawal to bank | PRD-003 | async (outbox + webhook) |
| P2P transfer | PRD-004 | sync |
| Reconciliation with bank | PRD-002/003 §Reconciliation, TDR-009 | background job |

## 7. Shared User Stories

- **US-1** As a new user, I register with phone number + password and a wallet with balance 0
  is created for me.
- **US-2** As a user, I log in and receive an access token used for all other calls.
- **US-3** As a user, I see my wallet: ID, currency, available balance, status.
- **US-4** As a user, I list my transactions (newest first, paginated by cursor) with type,
  amount, direction (in/out), status, counterparty (for P2P), created/updated time.
- **US-5** As a user, I open one transaction and see its current status and failure reason.
- **US-6** As a user, I link my bank account (IBAN + account holder name) so I can top up and
  withdraw. One linked bank account per user in this phase.

> **Open question Q2:** Authentication scope. Proposal (TDR-011): minimal built-in auth —
> phone + password (bcrypt), short-lived JWT access token. No OTP, no refresh tokens in this
> phase. Alternative: no auth at all, user ID in a header (dev only). Please choose.

## 8. Non-Functional Requirements

| ID | Requirement |
|----|-------------|
| NFR-1 | **Consistency:** a balance change and its ledger entry are written in one PostgreSQL ACID transaction. No partial state is possible. |
| NFR-2 | **Concurrency safety:** many parallel requests on the same wallet never overspend it (row-level locking, TDR-010). |
| NFR-3 | **Durability of bank work:** a request to the bank is stored (outbox) in the same DB transaction that creates the business transaction. A crash after commit never loses it. |
| NFR-4 | **At-least-once processing + idempotent handlers** for outbox events and webhooks = effectively-once money movement. |
| NFR-5 | **Eventual completion:** every `PENDING`/`PROCESSING` bank transaction reaches a terminal state within a bounded time (proposed: 24 h) through webhook, retries or reconciliation. Anything stuck longer is flagged for an operator. |
| NFR-6 | **Latency (p95, local env):** read APIs < 100 ms; P2P < 200 ms; top-up/withdraw request acceptance < 200 ms (bank work is async). |
| NFR-7 | **Observability:** structured JSON logs with request ID / transaction ID; Prometheus metrics for outbox lag, retries, webhook errors, reconciliation breaks. |
| NFR-8 | **Security:** passwords hashed with bcrypt; webhooks verified with HMAC signature; no secrets in code; amounts and IDs validated on every input. |
| NFR-9 | **Auditability:** ledger and webhook payloads retained (never deleted) in this phase. |
| NFR-10 | **Graceful shutdown:** on SIGTERM the service stops taking work, finishes in-flight jobs (bounded timeout) and exits; unfinished work is resumed by the next start. |

## 9. Success Criteria for this Phase

1. All three flows work end-to-end against the bank simulator.
2. Automated tests prove: double-click protection, concurrent overspend protection, lost
   webhook recovery via reconciliation, duplicate webhook safety, bank timeout retry safety,
   service crash between steps.
3. A ledger integrity check (all postings sum to 0; cached balances = ledger balances)
   passes after every test scenario.

## 10. Open Questions (summary)

| # | Question | Proposed answer |
|---|----------|-----------------|
| Q1 | Final transaction / balance / daily limits | Table in §5.6 |
| Q2 | Authentication scope | Minimal phone+password + JWT (TDR-011) |
| Q3 | Withdrawal: auto-retry on bank *rejection*? | No. Rejection = `FAILED` + refund. Only technical errors are retried (PRD-003). |
| Q4 | Can a user have more than one linked bank account? | No, one in this phase. |
| Q5 | Time to wait before reconciliation asks the bank about a stuck transaction | 10 minutes after it entered `PROCESSING` (TDR-009). |
| Q6 | Top-up model: bank **pulls** from the user's account (direct debit) vs. user **pushes** a bank transfer with a reference | Pull/direct-debit model (PRD-002). |

## 11. Glossary

| Term | Meaning |
|------|---------|
| Piaster (قرش) | 1/100 EGP; the minor unit we store. |
| Ledger | Append-only double-entry record of all money movements; system of record. |
| Journal entry | One balanced group of postings (debits = credits). |
| Posting | One line of a journal entry: an account, a direction (debit/credit), an amount. |
| Settlement account | The company's real bank account at the external bank. All user money in the wallets is backed by money in this account. |
| Outbox | DB table where work for external systems is stored in the same transaction as the business change; read by workers. |
| Inbox | DB table where incoming webhooks are stored first, then processed. |
| Reconciliation | Comparing our records with the bank's to find and fix differences. |
