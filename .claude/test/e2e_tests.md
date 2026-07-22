# End-to-End Tests

File: `internal/handler/wallet_e2e_test.go`
Package: `handler_test` (same package as the mocked handler tests — reuses their `doRequest`/`decodeError` helpers)

Nothing in this path is faked: real `gin` router → real `Handler` → real `WalletService` →
real `mysql.Repository` → real MySQL. This is the only place the full request path is
exercised in one test — the mocked handler tests stub out the service, and the repository
concurrency tests call the service directly, bypassing HTTP.

Gated by `TEST_DATABASE_DSN` exactly like the repository concurrency tests: `newE2ERouter(t)`
calls `t.Skip(...)` when it's unset, so `go test ./...` stays green with no external
dependency.

```
export TEST_DATABASE_DSN='root@tcp(127.0.0.1:3306)/wallet_test?parseTime=true&loc=UTC'
go test ./... -race
```

| Test | Scenario | Expected |
|---|---|---|
| `TestE2E_FullWalletLifecycle_OverHTTP` | POST create wallet → POST topup 500 → POST deduct 200 → GET balance → GET transactions, all as real HTTP requests | Each step returns the right status code; the `GET balance` response is 300 (500 − 200), read back from a value actually persisted in MySQL rather than just echoed by the write calls; transaction list has exactly 2 entries, newest (deduct, 200) first |
| `TestE2E_DuplicateWallet_OverHTTP` | POST create wallet for the same `customer_id` twice over HTTP | First: `201 Created`. Second: `409 Conflict`, `DUPLICATE_WALLET` — proves the DB's unique constraint on `customer_id` is reachable and correctly mapped through the full stack, not just asserted against a fake |
| `TestE2E_DeductIdempotentReplay_OverHTTP` | Top up 500, then POST the same deduct (amount 100, `order_id` `"order-1"`) twice over HTTP | Second response has `idempotent_replay: true`, identical transaction ID and balance to the first; a follow-up `GET balance` confirms 400 (not double-deducted) — proves the `(wallet_id, order_id, type)` unique constraint and replay lookup work together against real MySQL, not just in-process |
| `TestE2E_DeductInsufficientFunds_OverHTTP` | POST deduct 100 against a fresh zero-balance wallet | `422 Unprocessable Entity`, `INSUFFICIENT_FUNDS`; follow-up `GET balance` confirms it's still 0 |
| `TestE2E_TopUpWalletNotFound_OverHTTP` | POST topup against a syntactically valid but nonexistent wallet UUID | `404 Not Found`, `WALLET_NOT_FOUND` — proves a miss actually reaches the repository and maps correctly, not just a stubbed error |
