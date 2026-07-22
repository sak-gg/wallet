# Test Coverage Index

This service has tests at four layers. Full case lists live in the sibling files:

- [handler_tests.md](handler_tests.md) — HTTP layer, mocked service (`internal/handler/wallet_handler_test.go`), 14 cases
- [service_tests.md](service_tests.md) — business logic, mocked repository (`internal/service/wallet_service_test.go`), 16 cases
- [repository_concurrency_tests.md](repository_concurrency_tests.md) — real-MySQL concurrency/integration (`internal/repository/mysql/wallet_repository_concurrency_test.go`), 3 cases
- [e2e_tests.md](e2e_tests.md) — full stack over real HTTP + real MySQL, nothing faked (`internal/handler/wallet_e2e_test.go`), 5 cases

**Total: 38 test cases** across 4 files, plus one test-support fake (`internal/service/fake_repository_test.go`, an in-memory repository double — not a test itself).

## How they run

- `go test ./...` — runs handler + service unit tests (no external deps). The concurrency and e2e tests skip themselves (not fail) when `TEST_DATABASE_DSN` is unset.
- `TEST_DATABASE_DSN='root@tcp(127.0.0.1:3306)/wallet_test?parseTime=true&loc=UTC' go test ./... -race` — also runs the real-MySQL concurrency and e2e tests, with the race detector on.

## Layer summary

| Layer | File | Doubles used | What it proves |
|---|---|---|---|
| Handler | `wallet_handler_test.go` | `fakeService` (stubs the service interface) | HTTP status codes, error-code mapping, request validation/binding |
| Service | `wallet_service_test.go` | `fakeRepository` / `fakeLockedWallet` (in-memory) | Business rules: validation, balance math, idempotency bookkeeping |
| Repository | `wallet_repository_concurrency_test.go` | none — real MySQL | `SELECT ... FOR UPDATE` locking actually serializes concurrent writers |
| Full stack | `wallet_e2e_test.go` | none — real router, service, repository, MySQL | The whole request path is wired correctly: JSON binding → handler → service → repository → SQL → JSON response |

Note: `TopUp` takes an `order_id` (same as `Deduct`) for idempotency, scoped to
`(wallet_id, order_id, type)` — a topup and a deduct can safely reuse the same `order_id` value
without colliding.
