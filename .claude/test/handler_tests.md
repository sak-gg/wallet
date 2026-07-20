# Handler Tests

File: `internal/handler/wallet_handler_test.go`
Package: `handler_test`

HTTP-level tests. Each spins up `handler.NewRouter(handler.NewHandler(fake))` where `fake`
is a `fakeService` — a hand-rolled stub implementing the service interface via injectable
function fields (`createWalletFn`, `topUpFn`, `deductFn`, `getBalanceFn`, `listTransactionsFn`).
Requests are fired with `httptest` and assertions check status code and, where relevant, the
JSON error `code` field.

## Create Wallet — `POST /wallets`

| Test | Scenario | Expected |
|---|---|---|
| `TestCreateWallet_Success` | Valid `customer_id` in body | `201 Created` |
| `TestCreateWallet_DuplicateWallet` | Service returns `domain.ErrDuplicateWallet` | `409 Conflict`, error code `DUPLICATE_WALLET` |
| `TestCreateWallet_MalformedJSON` | Request body is invalid JSON (`{not-json`) | `400 Bad Request` |

## Top Up — `POST /wallets/{id}/topup`

| Test | Scenario | Expected |
|---|---|---|
| `TestTopUp_WalletNotFound` | Service returns `domain.ErrWalletNotFound` | `404 Not Found`, error code `WALLET_NOT_FOUND` |
| `TestTopUp_InvalidWalletIDInPath` | Path param is not a UUID (`not-a-uuid`) | `400 Bad Request` |
| `TestTopUp_InvalidAmount` | Service returns `domain.ErrInvalidAmount` (amount `0`) | `400 Bad Request`, error code `VALIDATION_ERROR` |
| `TestTopUp_FloatAmountRejected` | Body has a float amount (`100.5`) instead of an integer | `400 Bad Request`; asserts the service is never called (binding rejects it first) |

## Deduct — `POST /wallets/{id}/deduct`

| Test | Scenario | Expected |
|---|---|---|
| `TestDeduct_InsufficientFunds` | Service returns `domain.ErrInsufficientFunds` | `422 Unprocessable Entity`, error code `INSUFFICIENT_FUNDS` |
| `TestDeduct_Success` | Valid amount + `order_id` | `200 OK` |

## Get Balance — `GET /wallets/{id}/balance`

| Test | Scenario | Expected |
|---|---|---|
| `TestGetBalance_WalletNotFound` | Service returns `domain.ErrWalletNotFound` | `404 Not Found` |

## List Transactions — `GET /wallets/{id}/transactions`

| Test | Scenario | Expected |
|---|---|---|
| `TestListTransactions_InvalidLimit` | `limit=-1` query param | `400 Bad Request` |
| `TestListTransactions_Success` | No query params supplied | `200 OK`; asserts handler defaults to `limit=50, offset=0` |
