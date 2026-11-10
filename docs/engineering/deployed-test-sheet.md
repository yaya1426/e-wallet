# E-Wallet — Deployed Test Sheet (curl)

A step-by-step test of the full use case against the **deployed** wallet. It covers:
- registration and login;
- linking a bank account;
- top-up;
- transfer to another user (P2P);
- withdrawal;
- every bank outcome;
- the main errors;
- sessions and password reset;
- the operator tools.

Run the steps **in order, in one terminal** (bash or zsh), because later steps reuse variables. For each step, compare the output with **Expected** and tick the box.

| | |
|---|---|
| Endpoint | `https://shark-app-mjjpl.ondigitalocean.app` |
| Tools | `curl`, `jq`, `uuidgen` |
| Duration | about 10 minutes |
| Tester | ____________ |
| Date | ____________ |

**How the bank is simulated.** The bank is a simulator. The **last four digits of the linked IBAN** decide what it does:

| IBAN | Bank behaviour |
|------|----------------|
| `EG890003000100000000123450000` | Succeeds after ~2 s |
| `EG620003000100000000123450001` | Accepts, then fails after ~2 s: top-up `INSUFFICIENT_FUNDS`, withdrawal `ACCOUNT_CLOSED` |
| `EG350003000100000000123450002` | Refuses at once: `INVALID_ACCOUNT` |

**Amounts** are integer piasters: `100000` = 1,000.00 EGP.

**Limits to keep in mind:**
- **Registration:** at most **5 per IP per hour**. This sheet uses 3 attempts; running it twice within one hour fails at step 2.
- **Login:** at most 10 attempts per phone and 30 per IP per minute.

---

## 0. Setup

**How to paste:** paste each code block as **one** paste and wait for it to finish before the next. Every block in this sheet is safe to paste whole. Stay in the **same terminal window** from step 0.1 to the end: the variables and helpers below live only in that terminal session. If you open a new terminal, run section 0 again.

### 0.1 Check the tools
```bash
curl --version | head -1
jq --version
uuidgen
```
**Expected:** a curl version, a jq version, and a random UUID. If one is missing, install it first (`brew install jq` on macOS). ☐

### 0.2 Point at the deployed wallet
```bash
API=https://shark-app-mjjpl.ondigitalocean.app
curl -s $API/healthz
```
**Expected:** `{"status":"ok"}`. ☐

### 0.3 Choose the two test users
Fresh phone numbers every run, so the sheet can be repeated:
```bash
SUFFIX=$(( RANDOM % 9000 + 1000 ))$(( RANDOM % 9000 + 1000 ))
PHONE_A="+2010$SUFFIX"
PHONE_B="+2011$SUFFIX"
PASS_A="secret-pass-A1"
PASS_B="secret-pass-B1"
echo "A=$PHONE_A  B=$PHONE_B"
```
**Expected:** two phone numbers that differ only in the 5th digit, e.g. `A=+201012345678  B=+201112345678`. Write them down: ______________ ☐

### 0.4 The bank account numbers (IBANs)
The last four digits tell the bank simulator what to do (see the table above):
```bash
IBAN_OK=EG890003000100000000123450000
IBAN_FAIL_LATER=EG620003000100000000123450001
IBAN_REJECT=EG350003000100000000123450002
echo "$IBAN_OK $IBAN_FAIL_LATER $IBAN_REJECT"
```
**Expected:** the three IBANs are printed. ☐

### 0.5 The operator key (only for sections 11 and 12)
Ask the app owner for the `INTERNAL_API_KEY`, replace the placeholder, then paste. Never put the key in tickets or chat. Without it, skip sections 11 and 12.
```bash
OP_KEY="<paste the INTERNAL_API_KEY here>"
echo "key length: ${#OP_KEY}"
```
**Expected:** `key length: 48`. ☐

### 0.6 Helper commands
Four short helpers keep the later steps readable. Paste each block as one paste.

`json`: a `curl` that sends JSON:
```bash
json() { curl -s -H 'Content-Type: application/json' "$@"; }
```

`key`: a new Idempotency-Key (a UUID):
```bash
key() { uuidgen; }
```

`balance TOKEN`: prints the user's available balance:
```bash
balance() { curl -s "$API/v1/me/wallet" -H "Authorization: Bearer $1" | jq .available_balance_minor; }
```

`wait_tx TOKEN TRANSACTION_ID`: polls a transaction until the bank's answer arrives (`COMPLETED` or `FAILED`, at most 20 s):
```bash
wait_tx() {
  for i in $(seq 1 40); do
    T=$(curl -s "$API/v1/transactions/$2" -H "Authorization: Bearer $1")
    S=$(echo "$T" | jq -r .status)
    if [ "$S" = "COMPLETED" ] || [ "$S" = "FAILED" ]; then echo "$T" | jq '{status, failure_code, amount_minor}'; return; fi
    sleep 0.5
  done
  echo "still $S after 20 s"
}
```

### 0.7 Check the helpers
```bash
type json key balance wait_tx | grep -c function
key
```
**Expected:** `4` (in zsh the text differs, but each helper is listed as a function), then a UUID. ☐

---

## 1. Service health

```bash
curl -s $API/healthz | jq
curl -s $API/readyz | jq
```
**Expected:**
- `{"status":"ok"}`.
- `{"status":"ok","checks":{"database":"ok","migrations":"ok"}}`.

☐

---

## 2. Registration

### 2.1 Register user A and user B
```bash
json -X POST $API/v1/auth/register -d "{\"phone\":\"$PHONE_A\",\"full_name\":\"Ahmed Mohamed Ali\",\"password\":\"$PASS_A\"}" | jq
json -X POST $API/v1/auth/register -d "{\"phone\":\"$PHONE_B\",\"full_name\":\"Sara Hassan\",\"password\":\"$PASS_B\"}" | jq '{phone, full_name}'
```
**Expected:**
- **A:** `user_id`, `phone` (= `$PHONE_A`), `full_name`, `wallet_id` and `created_at`.
- **B:** `{"phone": "+2011…", "full_name": "Sara Hassan"}`.

☐

### 2.2 Register the same phone again
```bash
json -X POST $API/v1/auth/register -d "{\"phone\":\"$PHONE_A\",\"full_name\":\"Someone Else\",\"password\":\"another-pass\"}" | jq .error.code
```
**Expected:** `"PHONE_ALREADY_REGISTERED"` (HTTP 409). ☐

---

## 3. Login

### 3.1 Wrong password
```bash
json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_A\",\"password\":\"wrong-password\"}" | jq .error.code
```
**Expected:** `"INVALID_CREDENTIALS"` (HTTP 401). ☐

### 3.2 Log in A and B
```bash
LOGIN_A=$(json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_A\",\"password\":\"$PASS_A\"}")
LOGIN_B=$(json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_B\",\"password\":\"$PASS_B\"}")
A=$(echo "$LOGIN_A" | jq -r .access_token)
B=$(echo "$LOGIN_B" | jq -r .access_token)
echo "$LOGIN_A" | jq '{token_type, expires_in, refresh_expires_in, has_refresh: (.refresh_token | length > 0)}'
```
**Expected:** `{"token_type":"Bearer","expires_in":900,"refresh_expires_in":2592000,"has_refresh":true}`. ☐

### 3.3 No token
```bash
curl -s $API/v1/me/wallet | jq .error.code
```
**Expected:** `"UNAUTHENTICATED"` (HTTP 401). ☐

---

## 4. Wallet

```bash
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq
```
**Expected:** `wallet_id`, `"currency":"EGP"`, `"available_balance_minor":0`, `"status":"ACTIVE"`. ☐

---

## 5. Bank account

### 5.1 Top-up before linking an account
```bash
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":100000,"currency":"EGP"}' | jq .error.code
```
**Expected:** `"BANK_ACCOUNT_REQUIRED"` (HTTP 422). ☐

### 5.2 Invalid IBAN
```bash
json -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -d '{"iban":"EG123","holder_name":"Ahmed Ali"}' | jq .error.code
```
**Expected:** `"INVALID_IBAN"` (HTTP 400). ☐

### 5.3 Link the "success" IBAN
```bash
json -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -d "{\"iban\":\"$IBAN_OK\",\"holder_name\":\"Ahmed Ali\"}" | jq
curl -s $API/v1/me/bank-account -H "Authorization: Bearer $A" | jq .iban
```
**Expected:**
- `iban` = `EG890003000100000000123450000`, `holder_name` = `"Ahmed Ali"`, plus `updated_at`.
- The GET returns the same IBAN.

☐

---

## 6. Top-up (success)

### 6.1 Request a top-up of 1,000.00 EGP
```bash
TOPUP_KEY=$(key)
TOPUP=$(json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $TOPUP_KEY" -d '{"amount_minor":100000,"currency":"EGP"}')
echo "$TOPUP" | jq '{type, status, amount_minor, currency}'
TOPUP_ID=$(echo "$TOPUP" | jq -r .transaction_id)
balance $A
```
**Expected:**
- `{"type":"TOPUP","status":"PENDING","amount_minor":100000,"currency":"EGP"}` (HTTP 202).
- Balance right after: **`0`**. No money moves before the bank confirms.

☐

### 6.2 Wait for the bank
```bash
wait_tx $A $TOPUP_ID
balance $A
```
**Expected:**
- After ~2 s: `{"status":"COMPLETED","failure_code":null,"amount_minor":100000}`.
- Balance **`100000`**.

☐

### 6.3 Double click (same Idempotency-Key)
```bash
curl -s -i -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $TOPUP_KEY" \
  -H 'Content-Type: application/json' -d '{"amount_minor":100000,"currency":"EGP"}' | grep -iE '^HTTP|idempotent-replayed'
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $TOPUP_KEY" -d '{"amount_minor":100000,"currency":"EGP"}' | jq -r .transaction_id
echo "original: $TOPUP_ID"
balance $A
```
**Expected:**
- `HTTP/… 202` with `Idempotent-Replayed: true`.
- The same `transaction_id` as the original.
- Balance still **`100000`**: no second top-up.

☐

### 6.4 Same key, different amount
```bash
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $TOPUP_KEY" -d '{"amount_minor":200000,"currency":"EGP"}' | jq .error.code
```
**Expected:** `"IDEMPOTENCY_KEY_REUSED"` (HTTP 422). ☐

### 6.5 Input errors
```bash
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -d '{"amount_minor":100000,"currency":"EGP"}' | jq .error.code
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":0,"currency":"EGP"}' | jq .error.code
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":100000,"currency":"USD"}' | jq .error.code
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":999,"currency":"EGP"}' | jq .error.code
json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":100000' | jq .error.code
```
**Expected, in order:**
1. `"IDEMPOTENCY_KEY_REQUIRED"`
2. `"INVALID_AMOUNT"`
3. `"UNSUPPORTED_CURRENCY"`
4. `"AMOUNT_OUT_OF_RANGE"` (minimum 1,000 = 10.00 EGP)
5. `"MALFORMED_REQUEST"`

☐

---

## 7. Transfer to another user (P2P)

### 7.1 Recipient preview
```bash
curl -s "$API/v1/recipients?phone=$(echo $PHONE_B | sed 's/+/%2B/')" -H "Authorization: Bearer $A" | jq
```
**Expected:** `{"phone":"+2011…","masked_name":"Sara H."}`. ☐

### 7.2 Send 250.00 EGP from A to B
```bash
json -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" \
  -d "{\"recipient_phone\":\"$PHONE_B\",\"amount_minor\":25000,\"currency\":\"EGP\",\"note\":\"lunch\"}" | jq '{type, status, amount_minor, balance_after_minor, recipient, note}'
echo "A: $(balance $A)  B: $(balance $B)"
```
**Expected:**
- `{"type":"P2P","status":"COMPLETED","amount_minor":25000,"balance_after_minor":75000,"recipient":{"phone":"+2011…","masked_name":"Sara H."},"note":"lunch"}` (HTTP 201).
- `A: 75000  B: 25000`.

☐

### 7.3 Transfer errors
```bash
json -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d "{\"recipient_phone\":\"$PHONE_A\",\"amount_minor\":1000,\"currency\":\"EGP\"}" | jq .error.code
json -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"recipient_phone":"+201099999999","amount_minor":1000,"currency":"EGP"}' | jq .error.code
json -X POST $API/v1/transfers -H "Authorization: Bearer $B" -H "Idempotency-Key: $(key)" -d "{\"recipient_phone\":\"$PHONE_A\",\"amount_minor\":30000,\"currency\":\"EGP\"}" | jq .error.code
```
**Expected, in order:**
1. `"SELF_TRANSFER_NOT_ALLOWED"`
2. `"RECIPIENT_NOT_FOUND"` (unless that number happens to exist)
3. `"INSUFFICIENT_FUNDS"` (B has 250.00)

☐

### 7.4 History of both users
```bash
curl -s $API/v1/me/transactions -H "Authorization: Bearer $A" | jq '.items[] | {type, direction, amount_minor, status}'
curl -s $API/v1/me/transactions -H "Authorization: Bearer $B" | jq '.items[] | {type, direction, amount_minor, note, counterparty}'
```
**Expected:**
- **A:** the P2P as `OUT` 25000, then the top-up as `IN` 100000.
- **B:** the P2P as `IN` 25000 with note `lunch` and counterparty A (`masked_name` `"Ahmed M. A."`).

☐

---

## 8. Withdrawal (success)

```bash
W=$(json -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":20000,"currency":"EGP"}')
echo "$W" | jq '{type, status, amount_minor}'
W_ID=$(echo "$W" | jq -r .transaction_id)
balance $A
wait_tx $A $W_ID
balance $A
```
**Expected:**
- `{"type":"WITHDRAWAL","status":"PENDING","amount_minor":20000}` (HTTP 202).
- Balance right after: **`55000`**. The money is reserved at once.
- After ~2 s: `COMPLETED`.
- Balance stays **`55000`**.

☐

---

## 9. When the bank says no

### 9.1 Top-up that fails later (…0001)
```bash
json -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -d "{\"iban\":\"$IBAN_FAIL_LATER\",\"holder_name\":\"Ahmed Ali\"}" > /dev/null
T=$(json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":10000,"currency":"EGP"}' | jq -r .transaction_id)
wait_tx $A $T
balance $A
```
**Expected:**
- `{"status":"FAILED","failure_code":"INSUFFICIENT_FUNDS","amount_minor":10000}`.
- Balance still **`55000`**.

☐

### 9.2 Withdrawal that fails later (…0001), with refund
```bash
W=$(json -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":5000,"currency":"EGP"}' | jq -r .transaction_id)
balance $A
wait_tx $A $W
balance $A
```
**Expected:**
- Balance right after: **`50000`** (reserved).
- Then `{"status":"FAILED","failure_code":"ACCOUNT_CLOSED",…}`.
- Balance back to **`55000`** (refunded).

☐

### 9.3 Instant refusal (…0002)
```bash
json -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -d "{\"iban\":\"$IBAN_REJECT\",\"holder_name\":\"Ahmed Ali\"}" > /dev/null
T=$(json -X POST $API/v1/topups -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":10000,"currency":"EGP"}' | jq -r .transaction_id)
wait_tx $A $T
W=$(json -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":5000,"currency":"EGP"}' | jq -r .transaction_id)
wait_tx $A $W
balance $A
```
**Expected:**
- Both end `FAILED` with `"INVALID_ACCOUNT"` within a second.
- Balance **`55000`**: the withdrawal reserve was refunded.

☐

### 9.4 Withdraw more than the balance
```bash
json -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":1000000,"currency":"EGP"}' | jq .error.code
```
**Expected:** `"INSUFFICIENT_FUNDS"` (HTTP 422). ☐

Put the success IBAN back for the remaining steps:
```bash
json -X PUT $API/v1/me/bank-account -H "Authorization: Bearer $A" -d "{\"iban\":\"$IBAN_OK\",\"holder_name\":\"Ahmed Ali\"}" | jq -r .iban
```

---

## 10. Sessions (refresh tokens and logout)

### 10.1 Refresh and rotation
```bash
R1=$(echo "$LOGIN_B" | jq -r .refresh_token)
NEW=$(json -X POST $API/v1/auth/refresh -d "{\"refresh_token\":\"$R1\"}")
R2=$(echo "$NEW" | jq -r .refresh_token)
curl -s $API/v1/me/wallet -H "Authorization: Bearer $(echo "$NEW" | jq -r .access_token)" | jq .available_balance_minor
json -X POST $API/v1/auth/refresh -d "{\"refresh_token\":\"$R1\"}" | jq .error.code
curl -s -o /dev/null -w "%{http_code}\n" -X POST $API/v1/auth/refresh -H 'Content-Type: application/json' -d "{\"refresh_token\":\"$R2\"}"
```
**Expected:**
1. The new access token works: balance `25000`.
2. Reusing the old refresh token → `"INVALID_REFRESH_TOKEN"`.
3. Then `R2` also fails with `401`: reuse signs out the whole session, as protection against a stolen token.

☐

### 10.2 Logout
```bash
L=$(json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_B\",\"password\":\"$PASS_B\"}")
curl -s -o /dev/null -w "%{http_code}\n" -X POST $API/v1/auth/logout -H "Authorization: Bearer $(echo "$L" | jq -r .access_token)" \
  -H 'Content-Type: application/json' -d "{\"refresh_token\":\"$(echo "$L" | jq -r .refresh_token)\"}"
json -X POST $API/v1/auth/refresh -d "{\"refresh_token\":\"$(echo "$L" | jq -r .refresh_token)\"}" | jq .error.code
```
**Expected:**
- `204`.
- `"INVALID_REFRESH_TOKEN"`: the session has ended.

☐

---

## 11. Operator tools (needs `OP_KEY`)

### 11.1 Without the key
```bash
curl -s $API/internal/v1/health/ops | jq .error.code
```
**Expected:** `"UNAUTHENTICATED"` (HTTP 401). ☐

### 11.2 Ops health and discrepancies
```bash
curl -s $API/internal/v1/health/ops -H "X-Internal-Api-Key: $OP_KEY" | jq '{status, discrepancies, stuck, outbox, inbox}'
curl -s "$API/internal/v1/discrepancies?status=OPEN" -H "X-Internal-Api-Key: $OP_KEY" | jq '.items | length'
```
**Expected:**
- `"status":"ok"`, with all counts `0`.
- `0` open discrepancies.

`warning` can appear for a few minutes after a deploy, while a job hasn't run yet. Check the `jobs` list.

☐

### 11.3 Freeze and unfreeze A's wallet
```bash
A_WALLET=$(curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq -r .wallet_id)
curl -s -X POST $API/internal/v1/wallets/$A_WALLET/freeze -H "X-Internal-Api-Key: $OP_KEY" | jq .status
json -X POST $API/v1/transfers -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d "{\"recipient_phone\":\"$PHONE_B\",\"amount_minor\":1000,\"currency\":\"EGP\"}" | jq .error.code
json -X POST $API/v1/withdrawals -H "Authorization: Bearer $A" -H "Idempotency-Key: $(key)" -d '{"amount_minor":5000,"currency":"EGP"}' | jq .error.code
json -X POST $API/v1/transfers -H "Authorization: Bearer $B" -H "Idempotency-Key: $(key)" -d "{\"recipient_phone\":\"$PHONE_A\",\"amount_minor\":1000,\"currency\":\"EGP\"}" | jq .status
curl -s $API/v1/me/wallet -H "Authorization: Bearer $A" | jq '{status, available_balance_minor}'
curl -s -X POST $API/internal/v1/wallets/$A_WALLET/unfreeze -H "X-Internal-Api-Key: $OP_KEY" | jq .status
```
**Expected, in order:**
1. `"FROZEN"`.
2. `"WALLET_FROZEN"`: A can't send.
3. `"WALLET_FROZEN"`: A can't withdraw.
4. `"COMPLETED"`: A can still receive.
5. `{"status":"FROZEN","available_balance_minor":56000}`.
6. `"ACTIVE"`.

☐

---

## 12. Forgotten password (needs `OP_KEY`)

```bash
CODE=$(curl -s -X POST "$API/internal/v1/users/$(echo $PHONE_B | sed 's/+/%2B/')/password-reset-code" -H "X-Internal-Api-Key: $OP_KEY" | jq -r .code)
echo "code: $CODE"
json -X POST $API/v1/auth/password-reset -d "{\"phone\":\"$PHONE_B\",\"code\":\"000000\",\"new_password\":\"brand-new-pass\"}" | jq .error.code
curl -s -o /dev/null -w "%{http_code}\n" -X POST $API/v1/auth/password-reset -H 'Content-Type: application/json' \
  -d "{\"phone\":\"$PHONE_B\",\"code\":\"$CODE\",\"new_password\":\"brand-new-pass\"}"
json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_B\",\"password\":\"$PASS_B\"}" | jq .error.code
json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_B\",\"password\":\"brand-new-pass\"}" | jq .token_type
```
**Expected, in order:**
1. A 6-digit code.
2. `"INVALID_RESET_CODE"`: a wrong code. If the issued code happens to be `000000`, this step returns `204` instead; issue a new code and continue.
3. `204`.
4. The old password → `"INVALID_CREDENTIALS"`.
5. The new password → `"Bearer"`.

☐

---

## 13. Final state

```bash
echo "A: $(balance $A)"
B2=$(json -X POST $API/v1/auth/login -d "{\"phone\":\"$PHONE_B\",\"password\":\"brand-new-pass\"}" | jq -r .access_token)
echo "B: $(balance $B2)"
curl -s $API/v1/me/transactions -H "Authorization: Bearer $A" | jq '[.items[] | {type, status, failure_code}]'
```
**Expected:**

| User | Balance | Why |
|------|---------|-----|
| A | **56000** | 100000 top-up − 25000 P2P − 20000 withdrawal + 1000 received from B |
| B | **24000** | 25000 received − 1000 sent to A |

A's history, newest first:

| # | Type | Status | Failure code |
|---|------|--------|--------------|
| 1 | P2P | COMPLETED | — |
| 2 | WITHDRAWAL | FAILED | `INVALID_ACCOUNT` |
| 3 | TOPUP | FAILED | `INVALID_ACCOUNT` |
| 4 | WITHDRAWAL | FAILED | `ACCOUNT_CLOSED` |
| 5 | TOPUP | FAILED | `INSUFFICIENT_FUNDS` |
| 6 | WITHDRAWAL | COMPLETED | — |
| 7 | P2P | COMPLETED | — |
| 8 | TOPUP | COMPLETED | — |

If you skipped section 11.3, A has 55000 and B has 25000, and the first P2P row isn't there.

☐

---

## Result

| Section | Pass | Notes |
|---------|------|-------|
| 1 Health | ☐ | |
| 2 Registration | ☐ | |
| 3 Login | ☐ | |
| 4 Wallet | ☐ | |
| 5 Bank account | ☐ | |
| 6 Top-up | ☐ | |
| 7 P2P | ☐ | |
| 8 Withdrawal | ☐ | |
| 9 Bank refusals | ☐ | |
| 10 Sessions | ☐ | |
| 11 Operator tools | ☐ | |
| 12 Password reset | ☐ | |
| 13 Final state | ☐ | |
