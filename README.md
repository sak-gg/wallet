# Wallet Service

## Docs & design history

- DB schema design discussion: [.claude/chats/db-schema-design-discussion.md](.claude/chats/db-schema-design-discussion.md)
- Wallet service planning discussion: [.claude/chats/wallet-service-planning.md](.claude/chats/wallet-service-planning.md)
- Order Service integration mock stub: [examples/order_service_deduct_stub.go](examples/order_service_deduct_stub.go)
- Implementation plan: [.claude/plans/plan.md](.claude/plans/plan.md)
- Future scope plan (SDK, rate limiting, retries): [.claude/plans/future-plan.md](.claude/plans/future-plan.md)
- Test methodology: [.claude/test](.claude/test)
- DB schema (DBML source): [schema.dbml](schema.dbml)
- DB schema (diagram): [schema.png](schema.png)

## What it does

An HTTP API that owns customer wallet balances and a full transaction ledger. An external
Order Service calls this service to deduct funds when a customer places an order; if the
deduction fails (e.g. insufficient balance), the order is rejected.

## Stack

- Go, [Gin](https://github.com/gin-gonic/gin) for HTTP routing
- MySQL (InnoDB) via [GORM](https://gorm.io/) — pessimistic row-level locking
  (`SELECT ... FOR UPDATE`) for safe concurrent balance updates
- Money is stored as whole rupees, `int64` — no paise, decimals, or floats

## Setup

Requires Go 1.22+ and a running MySQL 8.0.16+ instance (needed for `CHECK` constraint
enforcement; the app-level validation is the primary guard either way).

```bash
mysql -u root -e "CREATE DATABASE wallet;"
```

The schema (tables, indexes, unique/check constraints, and the `transactions.wallet_id`
foreign key) is applied automatically at startup via GORM `AutoMigrate` — no separate
migration step or tool required. It's safe to run on every startup.

## Running the service

```bash
export DATABASE_DSN='root@tcp(127.0.0.1:3306)/wallet?parseTime=true&loc=UTC'
export PORT=8080  # optional, defaults to 8080
go run ./cmd/server
```

`DATABASE_DSN` must include `parseTime=true` so MySQL `DATETIME` columns scan into Go
`time.Time` values.

## API

All responses are JSON. Errors use a uniform envelope:

```json
{"error": {"code": "INSUFFICIENT_FUNDS", "message": "insufficient funds"}}
```

### Create a wallet

```bash
curl -X POST http://localhost:8080/wallets \
  -H 'Content-Type: application/json' \
  -d '{"customer_id": "cust-123"}'
```

`201` on success. `409 DUPLICATE_WALLET` if the customer already has one (one wallet per
customer). `400 VALIDATION_ERROR` if `customer_id` is empty.

### Top up (idempotent)

```bash
curl -X POST http://localhost:8080/wallets/{id}/topup \
  -H 'Content-Type: application/json' \
  -d '{"amount": 500, "order_id": "topup-ref-001"}'
```

`200` whether this is the first call or a retry — the response includes
`"idempotent_replay": true/false`. `order_id` here is a caller-supplied reference for the
topup itself (e.g. a payment/PSP reference), not an order — calling this again with the
same `order_id` for the same wallet is a no-op that returns the original result, never a
double credit. `400` for a non-positive amount or missing `order_id`, `404` if the wallet
doesn't exist.

### Deduct (idempotent)

```bash
curl -X POST http://localhost:8080/wallets/{id}/deduct \
  -H 'Content-Type: application/json' \
  -d '{"amount": 100, "order_id": "order-789"}'
```

`200` whether this is the first call or a retry — the response includes
`"idempotent_replay": true/false`. Calling this again with the same `order_id` for the same
wallet is a no-op that returns the original result, never a double deduction. `404` if the
wallet doesn't exist, `422 INSUFFICIENT_FUNDS` if the balance is too low, `400` for a
non-positive amount or missing `order_id`.

### Balance

```bash
curl http://localhost:8080/wallets/{id}/balance
```

### Transaction history

```bash
curl 'http://localhost:8080/wallets/{id}/transactions?limit=50&offset=0'
```

Ordered most-recent-first. `limit` defaults to 50, capped at 200.

## Design notes

- **Locking**: every balance-mutating request (`topup`, `deduct`) runs inside one DB
  transaction that takes `SELECT ... FOR UPDATE` on the wallet row first. Two concurrent
  requests against the *same* wallet serialize on that lock — the second only proceeds after
  the first commits.
- **Idempotency**: both `topup` and `deduct` check for an existing ledger row with the same
  `(wallet_id, order_id, type)` *inside* that same lock, before deciding whether to act.
  `order_id` doubles as a generic external-reference column — an order reference for deduct,
  a caller-supplied topup reference for topup — with `type` in the lookup and the unique
  constraint so the two never collide even if a caller reuses the same value for both. Because
  the wallet row is locked for the whole transaction, this check-then-act is race-free without
  any extra coordination. The DB-level unique constraint on `(wallet_id, order_id, type)` is a
  safety net in case a future code path narrows the lock's scope; the ledger insert happens
  *before* the balance update specifically so that if that safety net ever fires, the balance
  was never touched for that attempt.
- **No auth layer**: the Order Service is assumed to be a trusted internal caller (network
  boundary), not something this service enforces.

## Testing

```bash
go test ./...
```

Runs the service-layer unit tests (table-driven, against an in-memory fake repository) and
the handler tests (against a fake service). The MySQL-backed concurrency/integration tests
are skipped automatically unless `TEST_DATABASE_DSN` is set.

To also run those — the tests that actually prove the locking/idempotency design holds up
under real concurrent load:

```bash
mysql -u root -e "CREATE DATABASE wallet_test;"
export TEST_DATABASE_DSN='root@tcp(127.0.0.1:3306)/wallet_test?parseTime=true&loc=UTC'
go test ./... -race
```

`-race` proves there's no Go-level shared-memory race in the client code (all synchronization
is delegated to MySQL row locks); the concurrency tests' balance and ledger-count assertions
are what prove actual correctness under concurrent load — both checks matter together.
