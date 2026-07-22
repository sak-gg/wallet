package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"

	"wallet/internal/handler"
	"wallet/internal/repository/mysql"
	"wallet/internal/service"
)

// newE2ERouter wires the real router on top of the real handler, real
// service, and a real MySQL-backed repository — nothing in this path is
// faked. It's the only place these tests differ from the handler_test.go
// suite, which stubs the service, and the mysql package's concurrency
// tests, which bypass HTTP entirely. Skipped (not failed) when
// TEST_DATABASE_DSN isn't set, matching the mysql package's concurrency
// tests, so `go test ./...` stays green with no external dependency.
func newE2ERouter(t *testing.T) http.Handler {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping end-to-end HTTP test")
	}
	repo, err := mysql.NewRepository(dsn)
	if err != nil {
		t.Fatalf("connect to test mysql: %v", err)
	}
	if err := repo.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	svc := service.NewWalletService(repo)
	return handler.NewRouter(handler.NewHandler(svc))
}

func decodeBody(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode response body: %v (body=%s)", err, body)
	}
}

type e2eWalletResponse struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
	Balance    int64  `json:"balance"`
}

type e2eTransactionResponse struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	Amount       int64   `json:"amount"`
	BalanceAfter int64   `json:"balance_after"`
	OrderID      *string `json:"order_id"`
}

type e2eTopupOrDeductResponse struct {
	WalletID         string                 `json:"wallet_id"`
	Balance          int64                  `json:"balance"`
	Transaction      e2eTransactionResponse `json:"transaction"`
	IdempotentReplay bool                   `json:"idempotent_replay"`
}

type e2eBalanceResponse struct {
	WalletID   string `json:"wallet_id"`
	CustomerID string `json:"customer_id"`
	Balance    int64  `json:"balance"`
}

type e2eTransactionsResponse struct {
	WalletID     string                   `json:"wallet_id"`
	Transactions []e2eTransactionResponse `json:"transactions"`
}

// TestE2E_FullWalletLifecycle_OverHTTP drives create -> topup -> deduct ->
// balance -> transactions entirely through real HTTP requests against a
// real MySQL-backed service, proving the whole stack is wired correctly
// end to end (JSON binding, handler, service, repository, SQL, and back
// out as a JSON response) rather than each layer in isolation.
func TestE2E_FullWalletLifecycle_OverHTTP(t *testing.T) {
	r := newE2ERouter(t)
	customerID := "e2e-" + uuid.NewString()

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": customerID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create wallet: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var wallet e2eWalletResponse
	decodeBody(t, rec.Body.Bytes(), &wallet)
	if wallet.CustomerID != customerID || wallet.Balance != 0 {
		t.Fatalf("unexpected created wallet: %+v", wallet)
	}
	walletID := wallet.ID

	rec = doRequest(t, r, http.MethodPost, "/wallets/"+walletID+"/topup", map[string]any{"amount": 500, "order_id": "topup-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("topup: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var topup e2eTopupOrDeductResponse
	decodeBody(t, rec.Body.Bytes(), &topup)
	if topup.Balance != 500 || topup.IdempotentReplay {
		t.Fatalf("unexpected topup response: %+v", topup)
	}

	rec = doRequest(t, r, http.MethodPost, "/wallets/"+walletID+"/deduct", map[string]any{"amount": 200, "order_id": "order-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("deduct: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var deduct e2eTopupOrDeductResponse
	decodeBody(t, rec.Body.Bytes(), &deduct)
	if deduct.Balance != 300 || deduct.IdempotentReplay {
		t.Fatalf("unexpected deduct response: %+v", deduct)
	}

	rec = doRequest(t, r, http.MethodGet, "/wallets/"+walletID+"/balance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get balance: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var balance e2eBalanceResponse
	decodeBody(t, rec.Body.Bytes(), &balance)
	if balance.Balance != 300 || balance.CustomerID != customerID {
		t.Fatalf("balance read back over HTTP doesn't match what was written: %+v", balance)
	}

	rec = doRequest(t, r, http.MethodGet, "/wallets/"+walletID+"/transactions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list transactions: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var txns e2eTransactionsResponse
	decodeBody(t, rec.Body.Bytes(), &txns)
	if len(txns.Transactions) != 2 {
		t.Fatalf("expected 2 ledger entries, got %d: %+v", len(txns.Transactions), txns.Transactions)
	}
	// Newest first: the deduct was written after the topup.
	if txns.Transactions[0].Type != "deduct" || txns.Transactions[0].Amount != 200 {
		t.Fatalf("expected newest entry to be the 200 deduct, got %+v", txns.Transactions[0])
	}
	if txns.Transactions[1].Type != "topup" || txns.Transactions[1].Amount != 500 {
		t.Fatalf("expected oldest entry to be the 500 topup, got %+v", txns.Transactions[1])
	}
}

// TestE2E_DuplicateWallet_OverHTTP proves the DB's unique constraint on
// customer_id is actually reachable and correctly mapped to 409 through the
// full stack, not just asserted at the service layer against a fake.
func TestE2E_DuplicateWallet_OverHTTP(t *testing.T) {
	r := newE2ERouter(t)
	customerID := "e2e-" + uuid.NewString()

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": customerID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": customerID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "DUPLICATE_WALLET" {
		t.Fatalf("expected DUPLICATE_WALLET, got %q", code)
	}
}

// TestE2E_DeductIdempotentReplay_OverHTTP proves the (wallet_id, order_id,
// type) unique constraint and the replay lookup work together correctly
// against real MySQL when driven through HTTP, not just in-process.
func TestE2E_DeductIdempotentReplay_OverHTTP(t *testing.T) {
	r := newE2ERouter(t)
	customerID := "e2e-" + uuid.NewString()

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": customerID})
	var wallet e2eWalletResponse
	decodeBody(t, rec.Body.Bytes(), &wallet)

	doRequest(t, r, http.MethodPost, "/wallets/"+wallet.ID+"/topup", map[string]any{"amount": 500, "order_id": "topup-1"})

	rec = doRequest(t, r, http.MethodPost, "/wallets/"+wallet.ID+"/deduct", map[string]any{"amount": 100, "order_id": "order-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("first deduct: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var first e2eTopupOrDeductResponse
	decodeBody(t, rec.Body.Bytes(), &first)

	rec = doRequest(t, r, http.MethodPost, "/wallets/"+wallet.ID+"/deduct", map[string]any{"amount": 100, "order_id": "order-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("second deduct: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var second e2eTopupOrDeductResponse
	decodeBody(t, rec.Body.Bytes(), &second)

	if !second.IdempotentReplay {
		t.Fatalf("expected second deduct to be flagged as a replay: %+v", second)
	}
	if second.Transaction.ID != first.Transaction.ID {
		t.Fatalf("expected replay to return the original transaction id")
	}
	if second.Balance != first.Balance {
		t.Fatalf("expected replay balance %d to match original %d", second.Balance, first.Balance)
	}

	rec = doRequest(t, r, http.MethodGet, "/wallets/"+wallet.ID+"/balance", nil)
	var balance e2eBalanceResponse
	decodeBody(t, rec.Body.Bytes(), &balance)
	if balance.Balance != 400 {
		t.Fatalf("expected balance 400 after a single effective deduct despite 2 HTTP calls, got %d", balance.Balance)
	}
}

// TestE2E_DeductInsufficientFunds_OverHTTP proves the balance >= 0 CHECK
// constraint's application-level guard is reachable and correctly mapped to
// 422 through the full stack.
func TestE2E_DeductInsufficientFunds_OverHTTP(t *testing.T) {
	r := newE2ERouter(t)
	customerID := "e2e-" + uuid.NewString()

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": customerID})
	var wallet e2eWalletResponse
	decodeBody(t, rec.Body.Bytes(), &wallet)

	rec = doRequest(t, r, http.MethodPost, "/wallets/"+wallet.ID+"/deduct", map[string]any{"amount": 100, "order_id": "order-1"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "INSUFFICIENT_FUNDS" {
		t.Fatalf("expected INSUFFICIENT_FUNDS, got %q", code)
	}

	rec = doRequest(t, r, http.MethodGet, "/wallets/"+wallet.ID+"/balance", nil)
	var balance e2eBalanceResponse
	decodeBody(t, rec.Body.Bytes(), &balance)
	if balance.Balance != 0 {
		t.Fatalf("expected balance unchanged at 0, got %d", balance.Balance)
	}
}

// TestE2E_TopUpWalletNotFound_OverHTTP proves a well-formed but nonexistent
// wallet ID reaches the repository, misses, and maps to 404 through the
// full stack.
func TestE2E_TopUpWalletNotFound_OverHTTP(t *testing.T) {
	r := newE2ERouter(t)

	rec := doRequest(t, r, http.MethodPost, "/wallets/"+uuid.NewString()+"/topup", map[string]any{"amount": 500, "order_id": "topup-1"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "WALLET_NOT_FOUND" {
		t.Fatalf("expected WALLET_NOT_FOUND, got %q", code)
	}
}
