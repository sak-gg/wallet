# Order-Service Integration Stub

**You:** "this service is used by the order service, to do operations on wallet. write a mock
script showing the integration of this wallet service in the order service."

**Me:** First pass was overbuilt: a fully runnable demo (`examples/order_service_integration/main.go`)
wiring the real service/handler/router into an in-memory mock repo behind `httptest.Server`,
plus a client covering create/topup/deduct/balance and a full `main()` walking through the
whole order lifecycle.

**You:** "this is too much. we only want to show integration of /deduct API. and it is not
necessary to make the order service runnable. just show the integration part in a stub"

**Me:** Deleted that file and replaced it with a single non-runnable stub,
`examples/order_service_deduct_stub.go` (`package orderservice`, no `main()`, no mock server):
just `WalletClient.Deduct` (the real HTTP call to `POST /wallets/:id/deduct`) and
`ChargeForOrder`, the order-service call site showing `order_id` as the idempotency key and
`INSUFFICIENT_FUNDS` handled via `errors.As` on `WalletAPIError`. Verified with
`go build ./...` and `go vet ./...`.

Takeaway for next time: default to the smallest illustrative stub for "show the integration"
requests rather than a full runnable mock — ask or start minimal if scope is ambiguous.
