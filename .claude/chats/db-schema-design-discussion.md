# Wallet Service — DB Schema Design Discussion (2026-07-19)

**You:** "you have put balance in both tables. in every transaction, we'll have to update two
tables. what if we remove it from one table, maybe from walletRecord?"
**Me:** Explained `wallets.balance` is a read-optimized cache on top of `transactions.balance_after`,
both written in the same atomic DB transaction so they can't drift; removing it from `wallets`
would just relocate the read (tie-breaking + empty-wallet edge cases) with no real drop in
statement count. Left as-is.

**You:** "keep it like that only. another thing, should we introduce a new wallet ID, even when
customer:wallet is a 1:1 relationship. what if we directly use customerID as walletID too?
Doesn't sound like a good approach to me too, but I want to know your perspective"
**Me:** Agreed — a surrogate wallet ID keeps wallet identity decoupled from an externally-owned
`customer_id` (which can churn: format changes, account merges) and keeps the door open for
multi-wallet-per-customer later, at no lookup cost since `customer_id` already has a unique
index.

**You:** "we talked about balance fields. what if we remove it from transactions table?"
**Me:** This direction is more reasonable than the other one — `wallets.balance` stays the one
fast-read source of truth, no tie-breaking/empty-wallet complexity. Cost: lose the
point-in-time "balance right after this transaction" snapshot that ledger/audit systems often
keep. Left open pending a decision.

**You:** "why this index- idx_transactions_wallet_created\nis there in two fields?"
**Me:** Clarified it's one composite index over `(wallet_id, created_at)`, not two — needed so
`WHERE wallet_id=? ORDER BY created_at DESC` is served by a single index scan instead of a
separate filesort.

**You:** "why name it uq_transactions_wallet_order and not uq_transactions_order ?"
**Me:** (interrupted before answering)

**You:** "you have made a unique constraint uq_transactions_wallet_order on combination of
wallet and order. what if, in future, there is feature of returning orders, resulting in
refund of amount on the same order to the same wallet?"
**Me:** Flagged that the constraint as `(wallet_id, order_id)` would block a future refund row
for the same order (collides with the original deduct row); recommended widening it to
`(wallet_id, order_id, type)` so deduct and refund can coexist, each independently idempotent
per type — with the caveat that this only supports one refund per order.

**You:** "lets widen the constraint here"
**Me:** Widened `uq_transactions_wallet_order` to `(wallet_id, order_id, type)` using GORM
`priority` tags to keep `(wallet_id, order_id)` as the leftmost prefix (so the existing
idempotency lookup query stays index-efficient); verified against a fresh MySQL database that
the constraint covers all three columns, and the full test suite (including the `-race`
concurrency tests) still passes.
