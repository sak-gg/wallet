# Service Tests

File: `internal/service/wallet_service_test.go`
Package: `service_test`
Test double: `internal/service/fake_repository_test.go` — an in-memory `fakeRepository` /
`fakeLockedWallet` implementing the repository and `LockedWallet` interfaces (in-memory map,
mutex-protected `WithWalletLock`), so these tests run without a database.

Business-logic tests against `service.WalletService`, run sequentially (no concurrency claims
made at this layer — that's what the repository concurrency tests are for).

## Create Wallet

| Test | Scenario | Expected |
|---|---|---|
| `TestCreateWallet_DuplicateCustomerID` | Create a wallet for `cust-1`, then create again for the same customer ID | Second call returns `domain.ErrDuplicateWallet` |
| `TestCreateWallet_EmptyCustomerID` | `customer_id` is whitespace-only (`"   "`) | Returns `domain.ErrInvalidCustomerID` |

## Top Up

`TopUp` takes an `order_id` too (same idempotency pattern as `Deduct`), keyed on
`(wallet_id, order_id, type)` so a topup and a deduct can safely reuse the same `order_id` value.

| Test | Scenario | Expected |
|---|---|---|
| `TestTopUp_Success` | Top up a fresh wallet by 500 with `order_id` `"topup-1"` | Result balance `500`; transaction type `topup`, amount `500`; `IdempotentReplay` is `false`; a subsequent `GetBalance` confirms `500` was persisted |
| `TestTopUp_InvalidAmount` | Amounts `0`, `-1`, `-100` | Each returns `domain.ErrInvalidAmount` |
| `TestTopUp_MissingOrderID` | `order_id` is whitespace-only (`"   "`) | Returns `domain.ErrInvalidOrderID` |
| `TestTopUp_WalletNotFound` | Wallet ID `"missing-id"` | Returns `domain.ErrWalletNotFound` |
| `TestTopUp_IdempotentReplay` | Top up 500 with `order_id` `"topup-1"` twice | Second call's `IdempotentReplay` is `true`; balance and transaction ID match the first call; final balance `500` (not double-credited) |
| `TestTopUp_DeductSharingOrderID` | Top up 500 with `order_id` `"shared-id"`, then deduct 100 with the same `order_id` `"shared-id"` | Deduct is **not** treated as a replay (different type scopes the idempotency key independently); balance ends at `400` |

## Deduct

| Test | Scenario | Expected |
|---|---|---|
| `TestDeduct_Success` | Top up 500, then deduct 100 with `order-1` | Result balance `400`; `IdempotentReplay` is `false`; transaction's `OrderID` is `"order-1"` |
| `TestDeduct_InsufficientFunds` | Top up 50, then attempt to deduct 100 | Returns `domain.ErrInsufficientFunds`; balance stays `50` (unchanged — no partial effect) |
| `TestDeduct_IdempotentReplay` | Top up 500, deduct 100 with `order-1` twice | Second call's `IdempotentReplay` is `true`; second call's balance and transaction ID match the first exactly; ledger has exactly 1 deduct entry; final balance `400` (not double-deducted) |
| `TestDeduct_InvalidAmount` | Amounts `0`, `-1`, `-100` | Each returns `domain.ErrInvalidAmount` |
| `TestDeduct_MissingOrderID` | `order_id` is whitespace-only (`"   "`) | Returns `domain.ErrInvalidOrderID` |
| `TestDeduct_WalletNotFound` | Wallet ID `"missing-id"` | Returns `domain.ErrWalletNotFound` |

## Get Balance / List Transactions

| Test | Scenario | Expected |
|---|---|---|
| `TestGetBalance_WalletNotFound` | Wallet ID `"missing-id"` | Returns `domain.ErrWalletNotFound` |
| `TestListTransactions_WalletNotFound` | Wallet ID `"missing-id"` | Returns `domain.ErrWalletNotFound` |
