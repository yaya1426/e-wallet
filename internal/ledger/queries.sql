-- name: CreateAccount :exec
INSERT INTO ledger_accounts (id, code, kind, type, normal_side, created_at, updated_at)
VALUES (@id, @code, @kind, @type, @normal_side, @now, @now);

-- name: GetAccount :one
SELECT * FROM ledger_accounts WHERE id = @id;

-- name: GetAccountByCode :one
SELECT * FROM ledger_accounts WHERE code = @code;

-- name: LockAccounts :many
-- Ascending id order is the global lock order (TDR-010 §2).
SELECT * FROM ledger_accounts WHERE id = ANY(@ids::uuid[]) ORDER BY id FOR UPDATE;

-- name: InsertJournalEntry :exec
INSERT INTO journal_entries (id, transaction_id, entry_type, description, created_at)
VALUES (@id, @transaction_id, @entry_type, @description, @created_at);

-- name: ApplyPosting :one
-- One atomic statement per posting: a posting on the account's normal side increases the
-- cached balance, the other side decreases it (TDR-004 §4). The row stays locked until the
-- transaction ends; Post applies user-wallet accounts before system accounts (TDR-010 §3).
UPDATE ledger_accounts
   SET balance = balance + (CASE WHEN normal_side = @direction::text THEN 1 ELSE -1 END) * @amount::bigint,
       version = version + 1,
       updated_at = @now
 WHERE id = @id
RETURNING balance;

-- name: InsertPosting :exec
INSERT INTO postings (id, journal_entry_id, account_id, direction, amount, currency, balance_after, created_at)
VALUES (@id, @journal_entry_id, @account_id, @direction, @amount, @currency, @balance_after, @created_at);

-- name: PostingTotals :one
SELECT coalesce(sum(amount) FILTER (WHERE direction = 'DEBIT'), 0)::bigint  AS debits,
       coalesce(sum(amount) FILTER (WHERE direction = 'CREDIT'), 0)::bigint AS credits
  FROM postings;

-- name: AccountDrifts :many
-- Accounts whose cached balance differs from the balance computed from their postings.
SELECT a.id, a.code, a.balance AS cached,
       coalesce(sum(CASE WHEN p.direction = a.normal_side THEN p.amount ELSE -p.amount END), 0)::bigint AS computed
  FROM ledger_accounts a
  LEFT JOIN postings p ON p.account_id = a.id
 GROUP BY a.id
HAVING a.balance <> coalesce(sum(CASE WHEN p.direction = a.normal_side THEN p.amount ELSE -p.amount END), 0)
 ORDER BY a.id;

-- name: UnbalancedEntries :many
-- Journal entries that do not balance or have fewer than two postings.
SELECT e.id,
       coalesce(sum(p.amount) FILTER (WHERE p.direction = 'DEBIT'), 0)::bigint  AS debits,
       coalesce(sum(p.amount) FILTER (WHERE p.direction = 'CREDIT'), 0)::bigint AS credits,
       count(p.id) AS postings
  FROM journal_entries e
  LEFT JOIN postings p ON p.journal_entry_id = e.id
 GROUP BY e.id
HAVING count(p.id) < 2
    OR coalesce(sum(p.amount) FILTER (WHERE p.direction = 'DEBIT'), 0)
       <> coalesce(sum(p.amount) FILTER (WHERE p.direction = 'CREDIT'), 0)
 ORDER BY e.id;
