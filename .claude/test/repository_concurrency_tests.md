# Repository Concurrency / Integration Tests

File: `internal/repository/mysql/wallet_repository_concurrency_test.go`
Package: `mysql_test`

These are the only tests that run against a **real MySQL instance** rather than an in-memory
fake — they exist specifically to prove the pessimistic `SELECT ... FOR UPDATE` row-locking
design is correct under genuine concurrent load, which a sequential fake-repository test can't
exercise.

Gated by the `TEST_DATABASE_DSN` env var: `newTestRepository(t)` calls `t.Skip(...)` (not
`t.Fatal`) when it's unset, so `go test ./...` stays green with no external dependency. To
actually run these:

```
export TEST_DATABASE_DSN='root@tcp(127.0.0.1:3306)/wallet_test?parseTime=true&loc=UTC'
go test ./... -race
```

| Test | Scenario | Expected |
|---|---|---|
| `TestConcurrentDeduct_DistinctOrders_BalanceIntegrity` | Create a wallet, top up to exactly cover 50 concurrent deducts of 10 each (all with **distinct** `order_id`s), fire all 50 goroutines at once | No deduct errors; final balance is exactly `0` (no lost updates from the race); ledger has exactly 50 deduct entries |
| `TestConcurrentTopUp_SameOrderID_IdempotentUnderRace` | Create a fresh (zero-balance) wallet, then fire 20 goroutines all calling `TopUp` concurrently for 100 with the **same** `order_id` (simulates a retried top-up HTTP call) | No errors; every goroutine's result has the identical transaction ID and identical resulting balance (`100`); final balance is `100` (credited exactly once despite 20 concurrent callers); ledger has exactly 1 topup entry |
| `TestConcurrentDeduct_SameOrderID_IdempotentUnderRace` | Top up a wallet to 1000, then fire 20 goroutines all calling `Deduct` concurrently with the **same** `order_id` (simulates an Order Service retry storm) | No errors; every goroutine's result has the identical transaction ID and identical resulting balance (`900`); final balance is `900` (deducted exactly once despite 20 concurrent callers); ledger has exactly 1 deduct entry |
