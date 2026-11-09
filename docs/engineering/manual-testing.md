# Manual testing guide (phase 1)

This guide walks through the three phase-1 flows by hand against the local stack:
- **top-up:** bank → wallet;
- **P2P:** wallet → wallet;
- **withdrawal:** wallet → bank.

The bank is the **bank simulator** (`banksim`). It decides what happens from the **last four digits of the IBAN**:

| IBAN | What the bank does |
|------|--------------------|
| `EG890003000100000000123450000` | Succeeds after ~2 s (webhook `*.succeeded`) |
| `EG620003000100000000123450001` | Accepts, then fails after ~2 s: top-up `INSUFFICIENT_FUNDS`, withdrawal `ACCOUNT_CLOSED` |
| `EG350003000100000000123450002` | Refuses at once: `INVALID_ACCOUNT` |

Amounts are integer **piasters**: `50000` = 500.00 EGP.

You need `docker`, `make`, `curl` and `jq`. The commands work in `bash` and `zsh`. Run them in order in one shell, because later steps reuse the variables.

## 0. Start a clean stack

```bash
make down && make up
curl -s localhost:8080/readyz | jq
```
Expected: `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}`.

If port 5432 is taken on your machine, use `POSTGRES_HOST_PORT=5433 make up`.

## 1. Register and log in two users

```bash
API=http://localhost:8080
curl -s -X POST $API/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"phone":"+201001234567","full_name":"Ahmed Mohamed Ali","password":"secret-pass-1"}' | jq
curl -s -X POST $API/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"phone":"+201001234568","full_name":"Sara Hassan","password":"secret-pass-1"}' | jq '.full_name'

A=$(curl -s -X POST $API/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"phone":"+201001234567","password":"secret-pass-1"}' | jq -r .access_token)
B=$(curl -s -X POST $API/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"phone":"+201001234568","password":"secret-pass-1"}' | jq -r .access_token)
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq
```
Expected:
- The first register returns `201` with `user_id`, `phone`, `full_name`, `wallet_id` and `created_at`.
- `$A` and `$B` hold access tokens.
- The wallet shows `"available_balance_minor": 0`, `"status": "ACTIVE"`.

## 2. Link a bank account

```bash
curl -s -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -H 'Content-Type: application/json' \
  -d '{"iban":"EG890003000100000000123450000","holder_name":"Ahmed Ali"}' | jq
```
Expected: `200` with `iban`, `holder_name` and `updated_at`.

## 3. Top up (success)

Every money request needs an `Idempotency-Key` (a UUID). Repeating a request with the same key returns the first answer.

```bash
KEY=$(uuidgen)
TOPUP=$(curl -s -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' -d '{"amount_minor":50000,"currency":"EGP"}')
echo "$TOPUP" | jq
TOPUP_ID=$(echo "$TOPUP" | jq -r .transaction_id)
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
sleep 3
curl -s $API/v1/transactions/$TOPUP_ID -H "Authorization: Bearer $A" | jq '{status, direction, amount_minor}'
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
```
Expected:
- `202` with `"type":"TOPUP"`, `"status":"PENDING"`, `"amount_minor":50000`.
- Right after: balance `0`, because no money moves until the bank confirms.
- After ~2 s: `{"status":"COMPLETED","direction":"IN","amount_minor":50000}` and balance `50000`.

## 4. Idempotency replay (double click)

```bash
curl -s -i -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' -d '{"amount_minor":50000,"currency":"EGP"}' | grep -iE '^HTTP|idempotent-replayed|transaction_id'
curl -s -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $KEY" \
  -H 'Content-Type: application/json' -d '{"amount_minor":60000,"currency":"EGP"}' | jq .error.code
```
Expected:
- The same `202`, with `Idempotent-Replayed: true` and the same `transaction_id`. No second top-up is created.
- The same key with another amount gives `"IDEMPOTENCY_KEY_REUSED"`.

## 5. Send money to another user (P2P)

```bash
curl -s "$API/v1/recipients?phone=%2B201001234568" -H "Authorization: Bearer $A" | jq
curl -s -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' \
  -d '{"recipient_phone":"+201001234568","amount_minor":12500,"currency":"EGP","note":"lunch"}' | jq '{status, balance_after_minor, recipient}'
curl -s $API/v1/me/wallet -H "Authorization: Bearer $B" | jq .available_balance_minor
curl -s $API/v1/me/transactions -H "Authorization: Bearer $A" | jq '.items[] | {type, direction, amount_minor, status}'
curl -s $API/v1/me/transactions -H "Authorization: Bearer $B" | jq '.items[] | {type, direction, amount_minor, note}'
```
Expected:
- **Preview:** `{"phone":"+201001234568","masked_name":"Sara H."}`.
- **Transfer:** `201` with `{"status":"COMPLETED","balance_after_minor":37500,"recipient":{"phone":"+201001234568","masked_name":"Sara H."}}`.
- **B's balance:** `12500`.
- **A's history:** the P2P as `OUT` 12500, then the top-up as `IN` 50000.
- **B's history:** the P2P as `IN` 12500 with note `lunch`.

## 6. Withdraw (success)

```bash
W=$(curl -s -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":20000,"currency":"EGP"}')
echo "$W" | jq '{type, status, amount_minor}'
W_ID=$(echo "$W" | jq -r .transaction_id)
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
sleep 3
curl -s $API/v1/transactions/$W_ID -H "Authorization: Bearer $A" | jq '{status, direction}'
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
```
Expected:
- `202` with `{"type":"WITHDRAWAL","status":"PENDING","amount_minor":20000}`.
- Right after: balance `17500`. The amount is reserved at once.
- After ~2 s: `{"status":"COMPLETED","direction":"OUT"}`. The balance stays `17500`.

## 7. Rejections: the bank says no

Switch A's bank account to the "fails later" IBAN. A withdrawal is reserved, then refunded when the bank's failure arrives:

```bash
curl -s -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -H 'Content-Type: application/json' \
  -d '{"iban":"EG620003000100000000123450001","holder_name":"Ahmed Ali"}' > /dev/null
W=$(curl -s -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":5000,"currency":"EGP"}' | jq -r .transaction_id)
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
sleep 3
curl -s $API/v1/transactions/$W -H "Authorization: Bearer $A" | jq '{status, failure_code}'
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
```
Expected:
- Balance `12500` right after the request.
- After ~2 s: `{"status":"FAILED","failure_code":"ACCOUNT_CLOSED"}`, and the balance is back to `17500`.

Now the "refuses at once" IBAN, for a top-up:

```bash
curl -s -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -H 'Content-Type: application/json' \
  -d '{"iban":"EG350003000100000000123450002","holder_name":"Ahmed Ali"}' > /dev/null
T=$(curl -s -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":10000,"currency":"EGP"}' | jq -r .transaction_id)
sleep 1
curl -s $API/v1/transactions/$T -H "Authorization: Bearer $A" | jq '{status, failure_code}'
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq .available_balance_minor
```
Expected: `{"status":"FAILED","failure_code":"INVALID_ACCOUNT"}`; the balance is still `17500`.

With `…0001`, a top-up ends `FAILED` with `INSUFFICIENT_FUNDS`. With `…0002`, a withdrawal ends `FAILED` with `INVALID_ACCOUNT` and is refunded at once.

## 8. Business rules

```bash
curl -s -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":999999,"currency":"EGP"}' | jq .error.code
curl -s -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":500,"currency":"EGP"}' | jq .error.code
curl -s -X POST $API/v1/topups -H "Authorization: Bearer $B" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"amount_minor":50000,"currency":"EGP"}' | jq .error.code
curl -s -X POST $API/v1/topups -H "Authorization: Bearer $A" \
  -H 'Content-Type: application/json' -d '{"amount_minor":50000,"currency":"EGP"}' | jq .error.code
```
Expected:
- `"INSUFFICIENT_FUNDS"`: more than the balance.
- `"AMOUNT_OUT_OF_RANGE"`: below the 10.00 EGP minimum.
- `"BANK_ACCOUNT_REQUIRED"`: B has no linked account.
- `"IDEMPOTENCY_KEY_REQUIRED"`: no key.

## 9. Freeze a wallet

There is no operator API in phase 1, so freeze A's wallet with one SQL line:

```bash
docker compose exec -T postgres psql -U wallet -d wallet -c \
  "UPDATE wallets SET status = 'FROZEN' WHERE user_id = (SELECT id FROM users WHERE phone_e164 = '+201001234567')"
curl -s -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"recipient_phone":"+201001234568","amount_minor":1000,"currency":"EGP"}' | jq .error.code
curl -s -X POST $API/v1/transfers -H "Authorization: Bearer $B" -H "Idempotency-Key: $(uuidgen)" \
  -H 'Content-Type: application/json' -d '{"recipient_phone":"+201001234567","amount_minor":1000,"currency":"EGP"}' | jq .status
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq '{status, available_balance_minor}'
```
Expected:
- A cannot send: `"WALLET_FROZEN"`.
- A can still receive: B's transfer is `"COMPLETED"`.
- A's wallet shows `{"status":"FROZEN","available_balance_minor":18500}`.

## 10. Check the ledger

Every entry balances, and cached balances match the postings. Money reserved for withdrawals equals the withdrawals still in flight:

```bash
docker compose exec -T postgres psql -U wallet -d wallet -c "
SELECT (SELECT count(*) FROM (SELECT journal_entry_id FROM postings GROUP BY journal_entry_id
          HAVING sum(CASE WHEN direction = 'DEBIT' THEN amount ELSE -amount END) <> 0) x) AS unbalanced_entries,
       (SELECT count(*) FROM ledger_accounts a WHERE a.balance <> coalesce((SELECT sum(CASE WHEN p.direction = a.normal_side
          THEN p.amount ELSE -p.amount END) FROM postings p WHERE p.account_id = a.id), 0)) AS drifted_accounts,
       (SELECT balance FROM ledger_accounts WHERE code = 'WITHDRAWALS_IN_TRANSIT') AS in_transit,
       (SELECT balance FROM ledger_accounts WHERE code = 'BANK_SETTLEMENT') AS bank_settlement"
```
Expected: `unbalanced_entries 0`, `drifted_accounts 0`, `in_transit 0`, `bank_settlement 30000`: 50 000 topped up minus 20 000 paid out.

Stop the stack with `make down`.

## Automated version

`make up && make e2e` runs the same scenarios as Go tests (`test/e2e`). It checks the ledger after each one. CI runs it in the `smoke` job.

## What phase 1 does not do yet
- **Lost webhooks:** if the bank's webhook never arrives, the transaction stays `PROCESSING`. Phase 2 adds reconciliation, which asks the bank (`GET /v1/operations/{key}`) and applies the same outcome logic.
- **Dead outbox events:** a bank call that keeps failing technically ends `DEAD` after 10 attempts. The top-up or withdrawal then stays `PENDING`, with a withdrawal's money reserved, until phase 2's reconciliation resolves it.
- **Discrepancies:** contradictions are recorded in `reconciliation_discrepancies` (for example a bank result that conflicts with a final status, an amount mismatch, or an event for an unknown transaction). There is no operator API to list or resolve them yet.
- **Fault scenarios** `0003`–`0009` of the simulator, crash tests and load tests.
