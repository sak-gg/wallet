package service_test

import (
	"context"
	"errors"
	"testing"

	"wallet/internal/domain"
	"wallet/internal/service"
)

func newTestService() *service.WalletService {
	return service.NewWalletService(newFakeRepository())
}

func mustCreateWallet(t *testing.T, s *service.WalletService, customerID string) domain.Wallet {
	t.Helper()
	w, err := s.CreateWallet(context.Background(), customerID)
	if err != nil {
		t.Fatalf("CreateWallet(%q) unexpected error: %v", customerID, err)
	}
	return w
}

func TestCreateWallet_DuplicateCustomerID(t *testing.T) {
	s := newTestService()
	ctx := context.Background()

	mustCreateWallet(t, s, "cust-1")

	_, err := s.CreateWallet(ctx, "cust-1")
	if !errors.Is(err, domain.ErrDuplicateWallet) {
		t.Fatalf("expected ErrDuplicateWallet, got %v", err)
	}
}

func TestCreateWallet_EmptyCustomerID(t *testing.T) {
	s := newTestService()
	_, err := s.CreateWallet(context.Background(), "   ")
	if !errors.Is(err, domain.ErrInvalidCustomerID) {
		t.Fatalf("expected ErrInvalidCustomerID, got %v", err)
	}
}

func TestTopUp_Success(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")

	result, err := s.TopUp(ctx, w.ID, 500)
	if err != nil {
		t.Fatalf("TopUp unexpected error: %v", err)
	}
	if result.Balance != 500 {
		t.Fatalf("expected balance 500, got %d", result.Balance)
	}
	if result.Transaction.Type != domain.TransactionTypeTopup || result.Transaction.Amount != 500 {
		t.Fatalf("unexpected transaction record: %+v", result.Transaction)
	}

	balance, err := s.GetBalance(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetBalance unexpected error: %v", err)
	}
	if balance.Balance != 500 {
		t.Fatalf("expected persisted balance 500, got %d", balance.Balance)
	}
}

func TestTopUp_InvalidAmount(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")

	for _, amount := range []int64{0, -1, -100} {
		_, err := s.TopUp(ctx, w.ID, amount)
		if !errors.Is(err, domain.ErrInvalidAmount) {
			t.Fatalf("TopUp(%d) expected ErrInvalidAmount, got %v", amount, err)
		}
	}
}

func TestTopUp_WalletNotFound(t *testing.T) {
	s := newTestService()
	_, err := s.TopUp(context.Background(), "missing-id", 100)
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestDeduct_Success(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")
	if _, err := s.TopUp(ctx, w.ID, 500); err != nil {
		t.Fatalf("setup TopUp failed: %v", err)
	}

	result, err := s.Deduct(ctx, w.ID, 100, "order-1")
	if err != nil {
		t.Fatalf("Deduct unexpected error: %v", err)
	}
	if result.Balance != 400 {
		t.Fatalf("expected balance 400, got %d", result.Balance)
	}
	if result.IdempotentReplay {
		t.Fatalf("expected first deduct to not be a replay")
	}
	if result.Transaction.OrderID == nil || *result.Transaction.OrderID != "order-1" {
		t.Fatalf("expected transaction order_id order-1, got %+v", result.Transaction)
	}
}

func TestDeduct_InsufficientFunds(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")
	if _, err := s.TopUp(ctx, w.ID, 50); err != nil {
		t.Fatalf("setup TopUp failed: %v", err)
	}

	_, err := s.Deduct(ctx, w.ID, 100, "order-1")
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}

	balance, err := s.GetBalance(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetBalance unexpected error: %v", err)
	}
	if balance.Balance != 50 {
		t.Fatalf("expected balance unchanged at 50, got %d", balance.Balance)
	}
}

func TestDeduct_IdempotentReplay(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")
	if _, err := s.TopUp(ctx, w.ID, 500); err != nil {
		t.Fatalf("setup TopUp failed: %v", err)
	}

	first, err := s.Deduct(ctx, w.ID, 100, "order-1")
	if err != nil {
		t.Fatalf("first Deduct unexpected error: %v", err)
	}

	second, err := s.Deduct(ctx, w.ID, 100, "order-1")
	if err != nil {
		t.Fatalf("second Deduct unexpected error: %v", err)
	}
	if !second.IdempotentReplay {
		t.Fatalf("expected second call to be flagged as a replay")
	}
	if second.Balance != first.Balance {
		t.Fatalf("expected replay balance %d to match original %d", second.Balance, first.Balance)
	}
	if second.Transaction.ID != first.Transaction.ID {
		t.Fatalf("expected replay to return the original transaction id")
	}

	txns, err := s.ListTransactions(ctx, w.ID, 50, 0)
	if err != nil {
		t.Fatalf("ListTransactions unexpected error: %v", err)
	}
	deductCount := 0
	for _, txn := range txns {
		if txn.Type == domain.TransactionTypeDeduct {
			deductCount++
		}
	}
	if deductCount != 1 {
		t.Fatalf("expected exactly 1 deduct ledger entry, got %d", deductCount)
	}

	balance, err := s.GetBalance(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetBalance unexpected error: %v", err)
	}
	if balance.Balance != 400 {
		t.Fatalf("expected balance 400 after single effective deduct, got %d", balance.Balance)
	}
}

func TestDeduct_InvalidAmount(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")

	for _, amount := range []int64{0, -1, -100} {
		_, err := s.Deduct(ctx, w.ID, amount, "order-1")
		if !errors.Is(err, domain.ErrInvalidAmount) {
			t.Fatalf("Deduct(%d) expected ErrInvalidAmount, got %v", amount, err)
		}
	}
}

func TestDeduct_MissingOrderID(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	w := mustCreateWallet(t, s, "cust-1")

	_, err := s.Deduct(ctx, w.ID, 100, "   ")
	if !errors.Is(err, domain.ErrInvalidOrderID) {
		t.Fatalf("expected ErrInvalidOrderID, got %v", err)
	}
}

func TestDeduct_WalletNotFound(t *testing.T) {
	s := newTestService()
	_, err := s.Deduct(context.Background(), "missing-id", 100, "order-1")
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestGetBalance_WalletNotFound(t *testing.T) {
	s := newTestService()
	_, err := s.GetBalance(context.Background(), "missing-id")
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestListTransactions_WalletNotFound(t *testing.T) {
	s := newTestService()
	_, err := s.ListTransactions(context.Background(), "missing-id", 50, 0)
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}
