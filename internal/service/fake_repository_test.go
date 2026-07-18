package service_test

import (
	"context"
	"time"

	"wallet/internal/domain"
	"wallet/internal/service"
)

// fakeRepository is an in-memory WalletRepository test double. It is not
// safe for concurrent use — tests run sequentially, and real concurrency
// correctness is proven separately against MySQL in the repository package's
// integration tests.
type fakeRepository struct {
	wallets              map[string]domain.Wallet
	transactionsByWallet map[string][]domain.Transaction
	customerIndex        map[string]string
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		wallets:              map[string]domain.Wallet{},
		transactionsByWallet: map[string][]domain.Transaction{},
		customerIndex:        map[string]string{},
	}
}

func (f *fakeRepository) CreateWallet(ctx context.Context, wallet domain.Wallet) (domain.Wallet, error) {
	if _, exists := f.customerIndex[wallet.CustomerID]; exists {
		return domain.Wallet{}, domain.ErrDuplicateWallet
	}
	f.wallets[wallet.ID] = wallet
	f.customerIndex[wallet.CustomerID] = wallet.ID
	return wallet, nil
}

func (f *fakeRepository) GetWallet(ctx context.Context, id string) (domain.Wallet, error) {
	w, ok := f.wallets[id]
	if !ok {
		return domain.Wallet{}, domain.ErrWalletNotFound
	}
	return w, nil
}

func (f *fakeRepository) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error) {
	txns := f.transactionsByWallet[walletID]
	result := make([]domain.Transaction, 0, len(txns))
	for i := len(txns) - 1; i >= 0; i-- {
		result = append(result, txns[i])
	}
	if offset > len(result) {
		return []domain.Transaction{}, nil
	}
	end := offset + limit
	if end > len(result) {
		end = len(result)
	}
	return result[offset:end], nil
}

func (f *fakeRepository) WithWalletLock(ctx context.Context, walletID string, fn func(ctx context.Context, locked service.LockedWallet) error) error {
	w, ok := f.wallets[walletID]
	if !ok {
		return domain.ErrWalletNotFound
	}
	lw := &fakeLockedWallet{repo: f, wallet: w}
	if err := fn(ctx, lw); err != nil {
		return err
	}
	f.wallets[walletID] = lw.wallet
	return nil
}

type fakeLockedWallet struct {
	repo   *fakeRepository
	wallet domain.Wallet
}

func (l *fakeLockedWallet) Wallet() domain.Wallet { return l.wallet }

func (l *fakeLockedWallet) FindTransactionByOrderID(ctx context.Context, orderID string) (domain.Transaction, bool, error) {
	for _, t := range l.repo.transactionsByWallet[l.wallet.ID] {
		if t.OrderID != nil && *t.OrderID == orderID {
			return t, true, nil
		}
	}
	return domain.Transaction{}, false, nil
}

func (l *fakeLockedWallet) InsertTransaction(ctx context.Context, txn domain.Transaction) (domain.Transaction, bool, error) {
	if txn.OrderID != nil {
		for _, t := range l.repo.transactionsByWallet[l.wallet.ID] {
			if t.OrderID != nil && *t.OrderID == *txn.OrderID {
				return t, true, nil
			}
		}
	}
	l.repo.transactionsByWallet[l.wallet.ID] = append(l.repo.transactionsByWallet[l.wallet.ID], txn)
	return txn, false, nil
}

func (l *fakeLockedWallet) UpdateBalance(ctx context.Context, newBalance int64) error {
	l.wallet.Balance = newBalance
	l.wallet.UpdatedAt = time.Now().UTC()
	return nil
}
