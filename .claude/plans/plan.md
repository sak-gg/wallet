# Wallet Service — Implementation Plan

## Context

We need a standalone Go HTTP service ("Wallet Service") that owns customer wallet balances
and a full audit ledger. An external, out-of-scope Order Service calls it synchronously to
deduct ₹100 (or another amount) before confirming an order; if the deduction fails (e.g.
insufficient balance) the order is rejected. The project directory is currently empty
besides two Claude Code skill files defining engineering standards (money-handling
correctness, layered Go architecture, ask-before-assuming) — this is a from-scratch build.

Decisions locked in with the user before this plan was written:
1. **Storage**: MySQL (InnoDB), with pessimistic row-level locking (`SELECT ... FOR UPDATE`)
   for safe concurrent balance mutation.
2. **Idempotency**: Order Service passes its own `order_id` in the `/deduct` body. A repeat
   call with the same `wallet_id` + `order_id` is a no-op that replays the original result —
   enforced both in application logic (check-then-act inside the row lock) and via a DB
   unique constraint as a safety net.
3. **Money**: whole rupees only, `int64`, no paise/decimals/floats.
4. **Deduct amount**: flexible — `/deduct` takes `{"amount": ..., "order_id": ...}` just like
   `/topup` takes `{"amount": ...}`, not hardcoded to 100.
5. **Wallet cardinality**: one wallet per `customer_id` (unique constraint); a second create
   attempt for the same customer returns `409`.
6. **Deliverable scope**: Go source + table-driven unit tests + a `go test -race` concurrency
   test against a real (non-Docker, env-var-gated) MySQL + a README. No Docker, no
   migration framework — a single `schema.sql` applied at startup.
7. **HTTP framework**: Gin (`github.com/gin-gonic/gin`), not stdlib `net/http` — chosen for
   built-in JSON binding and route-param handling; the endpoint count didn't strictly require
   it, but it's a standard, well-maintained choice with no real downside here.
8. **Data access**: GORM (`gorm.io/gorm` + `gorm.io/driver/mysql`), not raw `database/sql` —
   GORM's `db.Transaction(...)` auto commit/rollback and `clause.Locking{Strength: "UPDATE"}`
   fully support the pessimistic-locking design with less transaction bookkeeping than manual
   `sql.Tx`. GORM-tagged models are kept private to `repository/mysql`, separate from the
   framework-free `domain` structs, so persistence concerns don't leak into service/handler.

## MySQL-specific considerations (vs. the originally-proposed Postgres design)

The design (pessimistic locking, unique-constraint idempotency safety net) is unchanged and
works identically on MySQL/InnoDB. Concrete differences to account for:

- **No `RETURNING` clause**: not needed either way — GORM populates `CreatedAt`/`UpdatedAt`
  on the Go struct at the time of `Create`/`Update`, so the values are available immediately
  without reading them back from the DB.
- **`CHECK` constraints** are only enforced from MySQL 8.0.16+; on older MySQL/MariaDB they're
  parsed but silently ignored. Not load-bearing here since app-level validation
  (`ErrInvalidAmount` etc.) is the actual guard — `CHECK` is defense-in-depth, not the
  primary mechanism.
- **No native UUID type** — stored as `CHAR(36)`, generated in Go via `google/uuid`. Simpler
  than `BINARY(16)` at this scale; `BINARY(16)` would be a production index-size optimization.
- **`ENGINE=InnoDB`** specified explicitly on every table — InnoDB is required for
  transactions and row-level locks (MyISAM supports neither); explicit rather than relying on
  the server default.
- **Duplicate-key detection is coarser**: under the hood the driver surfaces MySQL error 1062
  with a message string (no structured constraint name like pgx gives). With
  `gorm.Config{TranslateError: true}`, GORM translates this to `gorm.ErrDuplicatedKey`, which
  the repository checks via `errors.Is`. Distinguishing *which* unique constraint fired
  doesn't require parsing the message: each `INSERT` in this design is already scoped to one
  table/operation (`CreateWallet`'s insert vs. the deduct-flow's transaction insert), so the
  call site alone determines whether a duplicate maps to `ErrDuplicateWallet` or the
  idempotent-replay safety net.
- **Isolation level** defaults to REPEATABLE READ (vs. Postgres's READ COMMITTED) — doesn't
  affect correctness here: `SELECT ... FOR UPDATE` always locks the current committed row
  regardless of isolation level, and every lock in this design is a primary-key lookup (no
  range scan), so gap-locking is not a concern.

## Project structure

```
wallet/
  go.mod
  README.md
  cmd/server/main.go                               # wiring only
  internal/
    domain/
      wallet.go                                    # Wallet, Transaction structs
      errors.go                                    # sentinel errors shared by all layers
    config/config.go                               # Load() reads DATABASE_DSN / PORT from env
    service/
      wallet_service.go                             # business logic; declares WalletRepository interface
      wallet_service_test.go                        # table-driven, against an in-memory fake
      fake_repository_test.go
    repository/mysql/
      models.go                                     # GORM-tagged walletRecord/transactionRecord, TableName() overrides — schema source of truth
      wallet_repository.go                          # implements WalletRepository via gorm.io/gorm + gorm.io/driver/mysql
      wallet_repository_concurrency_test.go          # real-MySQL test, skipped w/o TEST_DATABASE_DSN
    handler/
      wallet_handler.go                              # declares WalletService interface; JSON transport only
      router.go                                      # builds *gin.Engine, 5 routes
      wallet_handler_test.go                          # httptest against a fake service
```

`domain` has zero deps so all layers can import it without cycles; sentinel errors live
there (not in `service`) so `repository/mysql` returns the same values the service branches
on with `errors.Is`, without a repository → service import. Per the "interfaces at the
consumer" rule, `service.WalletRepository` and `handler.WalletService` are each declared
where they're consumed, not in the implementing package.

## Tech stack

- **Routing**: Gin (`github.com/gin-gonic/gin`). `gin.New()` + `gin.Recovery()` middleware
  (panic recovery), custom minimal request logging if desired. Handlers use
  `c.ShouldBindJSON(&req)` for body decoding, `c.Param("id")` for the path variable,
  `c.JSON(status, body)` for responses.
- **Data access**: GORM (`gorm.io/gorm` + `gorm.io/driver/mysql`, which wraps
  `go-sql-driver/mysql`). Locked transactions via
  `db.Transaction(func(tx *gorm.DB) error { ... })` — GORM commits on a nil return and rolls
  back on error, so there's no manual `Commit`/`Rollback` bookkeeping. Row locking is explicit
  in code via `tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rec, "id = ?", id)`, which
  compiles to `SELECT ... FOR UPDATE` — the locking strategy stays visible, not hidden by ORM
  magic. Schema is defined once via GORM struct tags on the models in
  `repository/mysql/models.go` and applied with `db.AutoMigrate(&walletRecord{},
  &transactionRecord{})` at startup, so there's no separate `schema.sql` to keep in sync.
- **Config**: plain `os.Getenv` for `DATABASE_DSN` (required, e.g.
  `user:pass@tcp(127.0.0.1:3306)/wallet`) and `PORT` (default `8080`) — no config library for
  two variables.

## Database schema (`internal/repository/mysql/models.go`, GORM struct tags)

```go
type walletRecord struct {
    ID         string `gorm:"type:char(36);primaryKey"`
    CustomerID string `gorm:"type:varchar(255);not null;uniqueIndex:uq_wallets_customer_id"`
    Balance    int64  `gorm:"not null;default:0;check:balance >= 0"`
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
func (walletRecord) TableName() string { return "wallets" }

type transactionRecord struct {
    ID           string  `gorm:"type:char(36);primaryKey"`
    WalletID     string  `gorm:"type:char(36);not null;index:idx_transactions_wallet_created;uniqueIndex:uq_transactions_wallet_order"`
    Type         string  `gorm:"type:varchar(16);not null;check:type in ('topup','deduct')"`
    Amount       int64   `gorm:"not null;check:amount > 0"`
    BalanceAfter int64   `gorm:"not null;check:balance_after >= 0"`
    OrderID      *string `gorm:"type:varchar(255);uniqueIndex:uq_transactions_wallet_order"`
    CreatedAt    time.Time `gorm:"index:idx_transactions_wallet_created"`
}
func (transactionRecord) TableName() string { return "transactions" }
```

Explicit `TableName()` overrides avoid GORM's default pluralization guessing and keep names
matching what a DBA inspecting the database would expect (`wallets`, `transactions`), distinct
from the `walletRecord`/`transactionRecord` Go type names (which are deliberately not named
`Wallet`/`Transaction` to avoid confusion with the framework-free `domain.Wallet` /
`domain.Transaction` structs used everywhere outside this package). `CreatedAt`/`UpdatedAt`
use GORM's naming convention, which auto-populates them on create/update — no manual
timestamp handling needed. `uq_transactions_wallet_order` enforces idempotency only for
deducts: MySQL, like Postgres, treats `NULL <> NULL` for unique-index purposes, so unlimited
`(wallet_id, NULL)` topup rows remain allowed.

Applied via `db.AutoMigrate(&walletRecord{}, &transactionRecord{})` once at startup — GORM
creates tables/indexes/constraints from the tags if they don't exist, and is safe to re-run.
README documents this as the schema source of truth and shows `SHOW CREATE TABLE wallets;`
as the way to inspect what was actually applied. Note: `check` tag support requires MySQL
8.0.16+ to be *enforced* (older MySQL/MariaDB parses but ignores it) — not load-bearing since
app-level validation is the real guard.

## Endpoint specs

Common conventions: wallet IDs are validated as UUIDs in the **handler** before reaching the
service (malformed → `400`, well-formed-but-absent → `404`); `amount` is `int64` in request
structs so a JSON float fails at bind time as a free `400`; uniform error envelope
`{"error": {"code": "...", "message": "..."}}`.

**POST /wallets** — `{"customer_id": "cust-123"}` → `201`
`{"id","customer_id","balance":0,"created_at","updated_at"}`.
Errors: `400 VALIDATION_ERROR` (empty customer_id), `409 DUPLICATE_WALLET`.

**POST /wallets/:id/topup** — `{"amount": 500}` → `200`
`{"wallet_id","balance","transaction":{...}}`.
Errors: `400 VALIDATION_ERROR` (amount ≤ 0), `404 WALLET_NOT_FOUND`.
Flow (single `db.Transaction(func(tx *gorm.DB) error {...})`): locked read of the wallet
record (`tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(...)`) → not found ⇒ return
`ErrWalletNotFound` (GORM rolls back automatically); compute `newBalance`; `tx.Model(&wallet).
Update("balance", newBalance)`; `tx.Create(&transactionRecord{..., Type: "topup", OrderID:
nil, ...})`; return nil (GORM commits).

**POST /wallets/:id/deduct** — `{"amount": 100, "order_id": "order-789"}` → `200`
`{"wallet_id","balance","transaction":{...},"idempotent_replay": bool}` — **same shape**
whether first processing or a replay.
Errors: `400 VALIDATION_ERROR` (amount ≤ 0 or missing order_id), `404 WALLET_NOT_FOUND`,
`422 INSUFFICIENT_FUNDS`.
Flow, all inside the same `db.Transaction(...)` closure:
1. Locked read of the wallet record → not found ⇒ return `ErrWalletNotFound` (auto rollback).
2. **Idempotency check** (inside the lock): `tx.Where("wallet_id = ? AND order_id = ?",
   walletID, orderID).First(&existing)`. If found → do not touch balance, return existing
   record + current balance, `idempotent_replay=true`, return nil (no-op commit).
3. If `gorm.ErrRecordNotFound`: check `wallet.Balance >= amount`; if not, return
   `ErrInsufficientFunds` (auto rollback).
4. Else: `tx.Model(&wallet).Update("balance", wallet.Balance-amount)`; `tx.Create(&transactionRecord{
   ..., Type: "deduct", OrderID: &orderID, ...})`.
5. Safety net: if step 4's `Create` still returns `gorm.ErrDuplicatedKey` (should be
   structurally unreachable given the lock, but guards a future code path that narrows lock
   scope), re-query the existing row by `(wallet_id, order_id)` and return it as a replay
   instead of surfacing the raw error.
6. Return nil (GORM commits).

Because the wallet row is locked for the full transaction, two concurrent deduct calls with
the same `order_id` strictly serialize — the second only proceeds after the first commits,
so it always sees the step-2 row and correctly replays instead of double-deducting.

**GET /wallets/:id/balance** → `200 {"wallet_id","customer_id","balance"}`. Plain read, no
locking. Error: `404`.

**GET /wallets/:id/transactions** → `200 {"wallet_id","transactions":[...]}`, ordered
`created_at DESC`. Optional `?limit=&offset=` (default 50, cap 200) to bound response size —
not full cursor pagination (out of scope at this stage). Errors: `404`, `400` for
non-numeric/negative limit/offset.

## Error handling

Sentinel errors in `internal/domain/errors.go`: `ErrWalletNotFound`, `ErrDuplicateWallet`,
`ErrInsufficientFunds`, `ErrInvalidAmount`, `ErrInvalidCustomerID`, `ErrInvalidOrderID`.
`gorm.Config{TranslateError: true}` is set when opening the DB connection so GORM translates
raw driver errors into its own sentinels (`gorm.ErrRecordNotFound`, `gorm.ErrDuplicatedKey`).
The repository maps these to domain sentinels by call site (no message-parsing needed, since
each operation is already scoped to one table): `gorm.ErrRecordNotFound` on a wallet lookup →
`domain.ErrWalletNotFound`; `gorm.ErrDuplicatedKey` on `CreateWallet`'s insert →
`domain.ErrDuplicateWallet`; `gorm.ErrDuplicatedKey` on the deduct-flow's transaction insert →
handled inline as the idempotent-replay safety net (not surfaced as an error at all). Wrapped
with `fmt.Errorf("...: %w", domain.ErrX)` to preserve the chain; service and handler branch
with `errors.Is`. Handler has one central mapping: not-found→404, duplicate→409,
insufficient-funds→422, the three validation errors→400, default→log + generic 500.

## Testing strategy

**Service unit tests** — table-driven against an in-memory fake `WalletRepository` (map-based;
`WithWalletLock` runs the callback and keeps/discards mutations by returned error — no
internal thread-safety needed since tests run sequentially). Required cases: successful
deduct; insufficient balance (no mutation); duplicate `order_id` replay (second call returns
identical result, ledger has exactly one row); invalid amount (zero/negative) for both
topup and deduct; wallet not found for every method; duplicate `customer_id` on create.

**Handler tests** — `httptest` against a fake `WalletService` wired into the Gin router:
each sentinel error → correct status/JSON body, plus malformed JSON → 400.

**Concurrency/integration test** (`wallet_repository_concurrency_test.go`), the piece that
actually proves the locking design, against a **real** MySQL (`t.Skip` if
`TEST_DATABASE_DSN` unset — no Docker/testcontainers):
- Scenario A: N goroutines, each `Deduct` with a unique `order_id`, fixed amount, via
  `sync.WaitGroup`. Assert final balance == `start - N*amount` exactly and ledger has N rows
  — proves no lost updates across concurrent `FOR UPDATE` acquisitions.
- Scenario B: M goroutines all `Deduct` with the *same* `order_id` concurrently (simulating
  an Order Service retry storm). Assert balance drops by `amount` exactly once and every
  goroutine gets an identical response.
- Run everything with `go test -race`. Note for the write-up: `-race` proves no Go-level
  shared-memory race (all synchronization is delegated to MySQL row locks, no shared mutable
  state in the Go code) — it does not by itself prove balance correctness; Scenario A/B's
  assertions are what prove that. Both are required together.

README documents: local MySQL setup, `CREATE DATABASE wallet_test`,
`export TEST_DATABASE_DSN='user:pass@tcp(127.0.0.1:3306)/wallet_test'`, then
`go test ./... -race`.

## Task breakdown

1. **Scaffold module** — `go mod init`, package skeleton, placeholder `main.go`.
   *Done when*: `go build ./...` succeeds.
2. **Domain package** — structs + sentinel errors. Depends on 1.
3. **Models + repository layer** — GORM-tagged `walletRecord`/`transactionRecord` in
   `models.go`, `mysql.Repository` (gorm.io/gorm + gorm.io/driver/mysql, `AutoMigrate` at
   startup) with `CreateWallet`, `GetWallet`, `WithWalletLock`, `ListTransactions`,
   `FindTransactionByOrderID`, `gorm.ErrDuplicatedKey`/`gorm.ErrRecordNotFound` → sentinel
   mapping. *Done when*: manual smoke test against local MySQL creates/tops-up/reads a
   wallet. Depends on 2.
4. **Service layer + unit tests** — `WalletRepository` interface + `WalletService` with the
   validation/locking-callback logic above; all 6 table-driven cases. *Done when*:
   `go test ./internal/service/...` passes. Depends on 2 (can proceed in parallel with 3,
   since tests use a fake).
5. **HTTP handlers + router + wiring** — `WalletService` interface, 5 Gin handlers,
   error→status mapping, `router.go`, `config.Load()`, `main.go` wiring. *Done when*:
   `go run ./cmd/server` answers curl requests for all 5 endpoints against local MySQL.
   Depends on 3, 4.
6. **Handler tests** — `httptest` against a fake service, through the Gin router. Depends on
   5 (can be written alongside it).
7. **Concurrency/integration tests** — Scenarios A & B against `TEST_DATABASE_DSN`, clean
   skip when unset. *Done when*: `TEST_DATABASE_DSN=... go test ./... -race` passes locally
   and plain `go test ./...` still passes without the env var. Depends on 3, 4, 5.
8. **README** — MySQL setup, schema application, env vars, run instructions, unit vs.
   integration test invocation, example curl for all 5 endpoints, short design-notes section
   on locking/idempotency and the MySQL-specific notes above. Depends on 1–7, finalized last.

## Open tradeoffs (flagged, not hidden)

- Pessimistic `FOR UPDATE` over optimistic/version-column locking: makes the idempotency
  check-then-act atomic in one transaction with no retry-on-conflict loop, at the cost of
  holding a row lock per request — acceptable at per-wallet request rates.
- No separate idempotency-key table: `order_id` already is the natural key; the
  `transactions` ledger already records what a separate table would duplicate.
- `GET /transactions` gets a bounded `limit`/`offset`, not true cursor pagination — deferred.
- No auth layer: Order Service assumed a trusted internal caller (network boundary);
  flagged rather than silently assumed.
- App-generated UUIDs (`google/uuid`) instead of DB-side generation: MySQL has no native
  UUID type, and generating in Go keeps ID creation testable without a round trip.
  `created_at`/`updated_at` are left to GORM's standard convention-based auto-population.
- Both app-layer and DB-layer idempotency checks are intentionally redundant: the DB unique
  constraint is a safety net against a future bug narrowing lock scope, not strictly needed
  today.
- Schema via `AutoMigrate` at startup instead of a migration tool: fine for one schema
  version per agreed scope; would need real migrations (e.g. `golang-migrate`) the moment
  the schema changes after data exists — `AutoMigrate` only creates/adds, it won't safely
  handle destructive changes.
- `422` for insufficient funds vs `409` for duplicate wallet: keeps "business-rule violation
  on an otherwise-valid request" distinct from "conflicts with an existing unique resource."
- Gin over stdlib `net/http`: adds one dependency in exchange for built-in JSON
  binding/route-param ergonomics; not a correctness-relevant choice either way.
- GORM over raw `database/sql`: trades a small amount of query transparency for less
  transaction/scan boilerplate; the locking clause and transaction boundary are kept
  explicit in code (not hidden behind auto-generated queries) specifically to preserve the
  auditability this system's money-handling standard calls for.

## Verification

- `go vet ./...` and `go build ./...` clean.
- `go test ./...` passes with no `TEST_DATABASE_DSN` set (concurrency test cleanly skips).
- Against a local MySQL: `go run ./cmd/server`, then curl through the full lifecycle —
  create wallet → topup → balance → deduct → balance → repeat the same deduct request
  (same `order_id`) and confirm it replays without double-deducting → transactions list
  shows the expected ledger.
- `TEST_DATABASE_DSN='user:pass@tcp(127.0.0.1:3306)/wallet_test' go test ./... -race`
  passes, proving both no Go-level races and correct balances/idempotency under real
  concurrent load (Scenarios A & B).

### Critical files
- `internal/repository/mysql/models.go`
- `internal/domain/errors.go`
- `internal/repository/mysql/wallet_repository.go`
- `internal/service/wallet_service.go`
- `internal/handler/wallet_handler.go`
- `cmd/server/main.go`
