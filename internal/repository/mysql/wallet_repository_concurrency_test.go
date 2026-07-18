package mysql_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"

	"wallet/internal/domain"
	"wallet/internal/repository/mysql"
	"wallet/internal/service"
)

// newTestRepository connects to a real MySQL instance for the concurrency
// tests below, which are the only thing that actually proves the pessimistic
// SELECT ... FOR UPDATE locking design is correct under real concurrent
// load — the service-layer unit tests use an in-memory fake and run
// sequentially, so they can't exercise this. Skipped (not failed) when
// TEST_DATABASE_DSN isn't set, so `go test ./...` stays green with no
// external dependency.
func newTestRepository(t *testing.T) *mysql.Repository {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping MySQL concurrency/integration test")
	}
	repo, err := mysql.NewRepository(dsn)
	if err != nil {
		t.Fatalf("connect to test mysql: %v", err)
	}
	if err := repo.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return repo
}

// TestConcurrentDeduct_DistinctOrders_BalanceIntegrity fires many concurrent
// deducts with distinct order_ids at the same wallet and asserts no lost
// updates: every deduct's row lock must serialize against the others so the
// final balance and ledger row count are exact, not a product of a race.
func TestConcurrentDeduct_DistinctOrders_BalanceIntegrity(t *testing.T) {
	repo := newTestRepository(t)
	svc := service.NewWalletService(repo)
	ctx := context.Background()

	const (
		n      = 50
		amount = int64(10)
	)
	startBalance := int64(n) * amount

	wallet, err := svc.CreateWallet(ctx, "concurrency-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if _, err := svc.TopUp(ctx, wallet.ID, startBalance); err != nil {
		t.Fatalf("top up: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.Deduct(ctx, wallet.ID, amount, fmt.Sprintf("order-%d", i))
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("deduct %d failed: %v", i, err)
		}
	}

	final, err := svc.GetBalance(ctx, wallet.ID)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if final.Balance != 0 {
		t.Fatalf("expected balance 0 after %d concurrent deducts of %d from %d, got %d", n, amount, startBalance, final.Balance)
	}

	txns, err := svc.ListTransactions(ctx, wallet.ID, 200, 0)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	deductCount := 0
	for _, txn := range txns {
		if txn.Type == domain.TransactionTypeDeduct {
			deductCount++
		}
	}
	if deductCount != n {
		t.Fatalf("expected %d deduct ledger entries, got %d", n, deductCount)
	}
}

// TestConcurrentDeduct_SameOrderID_IdempotentUnderRace simulates an Order
// Service retry storm: many goroutines call Deduct concurrently with the
// SAME order_id. Exactly one must actually take effect; every caller must
// observe the same transaction and resulting balance.
func TestConcurrentDeduct_SameOrderID_IdempotentUnderRace(t *testing.T) {
	repo := newTestRepository(t)
	svc := service.NewWalletService(repo)
	ctx := context.Background()

	const (
		m      = 20
		amount = int64(100)
		start  = int64(1000)
	)

	wallet, err := svc.CreateWallet(ctx, "concurrency-test-"+uuid.NewString())
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if _, err := svc.TopUp(ctx, wallet.ID, start); err != nil {
		t.Fatalf("top up: %v", err)
	}

	orderID := "shared-order-" + uuid.NewString()

	var wg sync.WaitGroup
	results := make([]service.DeductResult, m)
	errs := make([]error, m)
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := svc.Deduct(ctx, wallet.ID, amount, orderID)
			results[i] = result
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("deduct %d failed: %v", i, err)
		}
	}

	firstTxnID := results[0].Transaction.ID
	for i, result := range results {
		if result.Transaction.ID != firstTxnID {
			t.Fatalf("goroutine %d got transaction id %s, expected %s (same as goroutine 0) for the same order_id",
				i, result.Transaction.ID, firstTxnID)
		}
		if result.Balance != start-amount {
			t.Fatalf("goroutine %d observed balance %d, expected %d", i, result.Balance, start-amount)
		}
	}

	final, err := svc.GetBalance(ctx, wallet.ID)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if final.Balance != start-amount {
		t.Fatalf("expected balance %d after a single effective deduct despite %d concurrent calls, got %d",
			start-amount, m, final.Balance)
	}

	txns, err := svc.ListTransactions(ctx, wallet.ID, 200, 0)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	deductCount := 0
	for _, txn := range txns {
		if txn.Type == domain.TransactionTypeDeduct {
			deductCount++
		}
	}
	if deductCount != 1 {
		t.Fatalf("expected exactly 1 deduct ledger entry despite %d concurrent calls with the same order_id, got %d", m, deductCount)
	}
}
