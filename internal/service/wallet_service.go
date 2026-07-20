package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"wallet/internal/domain"
)

// LockedWallet gives the callback passed to WithWalletLock access to the
// wallet row that is locked for the duration of the surrounding transaction,
// plus the ledger operations that must happen inside that same lock.
type LockedWallet interface {
	Wallet() domain.Wallet
	// FindTransactionByOrderID looks up an existing ledger row scoped to both
	// orderID and txnType: order_id is shared across transaction types (a
	// topup's idempotency key and a deduct's order reference occupy the same
	// column), so the type filter is required to avoid one type's row
	// satisfying a lookup for another type sharing the same order_id value.
	FindTransactionByOrderID(ctx context.Context, orderID string, txnType domain.TransactionType) (domain.Transaction, bool, error)
	// InsertTransaction returns (existing, true, nil) instead of an error if a
	// unique-constraint safety net fires on (wallet_id, order_id) — this should
	// be structurally unreachable given the row lock held for the transaction,
	// but guards against a future code path that narrows the lock's scope.
	InsertTransaction(ctx context.Context, txn domain.Transaction) (record domain.Transaction, safetyNetReplay bool, err error)
	UpdateBalance(ctx context.Context, newBalance int64) error
}

// WalletRepository is the persistence dependency of WalletService, declared
// here at the consumer per Go convention rather than in the implementing
// repository package.
type WalletRepository interface {
	CreateWallet(ctx context.Context, wallet domain.Wallet) (domain.Wallet, error)
	GetWallet(ctx context.Context, id string) (domain.Wallet, error)
	ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error)
	// WithWalletLock locks the wallet row for the duration of fn (SELECT ...
	// FOR UPDATE), invoking fn only if the wallet exists. Returns
	// domain.ErrWalletNotFound without calling fn if it doesn't. Commits if fn
	// returns nil, rolls back and propagates fn's error otherwise.
	WithWalletLock(ctx context.Context, walletID string, fn func(ctx context.Context, locked LockedWallet) error) error
}

type TopUpResult struct {
	WalletID         string
	Balance          int64
	Transaction      domain.Transaction
	IdempotentReplay bool
}

type DeductResult struct {
	WalletID         string
	Balance          int64
	Transaction      domain.Transaction
	IdempotentReplay bool
}

type WalletService struct {
	repo WalletRepository
}

func NewWalletService(repo WalletRepository) *WalletService {
	return &WalletService{repo: repo}
}

func (s *WalletService) CreateWallet(ctx context.Context, customerID string) (domain.Wallet, error) {
	if strings.TrimSpace(customerID) == "" {
		return domain.Wallet{}, domain.ErrInvalidCustomerID
	}

	now := time.Now().UTC()
	wallet := domain.Wallet{
		ID:         uuid.NewString(),
		CustomerID: customerID,
		Balance:    0,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	return s.repo.CreateWallet(ctx, wallet)
}

func (s *WalletService) TopUp(ctx context.Context, walletID string, amount int64, orderID string) (TopUpResult, error) {
	if amount <= 0 {
		return TopUpResult{}, domain.ErrInvalidAmount
	}
	if strings.TrimSpace(orderID) == "" {
		return TopUpResult{}, domain.ErrInvalidOrderID
	}

	var result TopUpResult
	err := s.repo.WithWalletLock(ctx, walletID, func(ctx context.Context, locked LockedWallet) error {
		wallet := locked.Wallet()

		if existing, found, err := locked.FindTransactionByOrderID(ctx, orderID, domain.TransactionTypeTopup); err != nil {
			return err
		} else if found {
			result = TopUpResult{
				WalletID:         walletID,
				Balance:          wallet.Balance,
				Transaction:      existing,
				IdempotentReplay: true,
			}
			return nil
		}

		newBalance := wallet.Balance + amount
		oid := orderID
		txn := domain.Transaction{
			ID:           uuid.NewString(),
			WalletID:     walletID,
			Type:         domain.TransactionTypeTopup,
			Amount:       amount,
			BalanceAfter: newBalance,
			OrderID:      &oid,
			CreatedAt:    time.Now().UTC(),
		}

		// Insert before mutating the balance: if this hits the safety-net
		// unique-constraint replay, the balance is never touched for this
		// attempt, so a duplicate can never be double-credited even in that
		// (structurally unreachable) path. Mirrors Deduct's ordering.
		inserted, safetyNetReplay, err := locked.InsertTransaction(ctx, txn)
		if err != nil {
			return err
		}
		if safetyNetReplay {
			result = TopUpResult{
				WalletID:         walletID,
				Balance:          wallet.Balance,
				Transaction:      inserted,
				IdempotentReplay: true,
			}
			return nil
		}

		if err := locked.UpdateBalance(ctx, newBalance); err != nil {
			return err
		}

		result = TopUpResult{WalletID: walletID, Balance: newBalance, Transaction: inserted, IdempotentReplay: false}
		return nil
	})
	if err != nil {
		return TopUpResult{}, err
	}
	return result, nil
}

func (s *WalletService) Deduct(ctx context.Context, walletID string, amount int64, orderID string) (DeductResult, error) {
	if amount <= 0 {
		return DeductResult{}, domain.ErrInvalidAmount
	}
	if strings.TrimSpace(orderID) == "" {
		return DeductResult{}, domain.ErrInvalidOrderID
	}

	var result DeductResult
	err := s.repo.WithWalletLock(ctx, walletID, func(ctx context.Context, locked LockedWallet) error {
		wallet := locked.Wallet()

		if existing, found, err := locked.FindTransactionByOrderID(ctx, orderID, domain.TransactionTypeDeduct); err != nil {
			return err
		} else if found {
			result = DeductResult{
				WalletID:         walletID,
				Balance:          wallet.Balance,
				Transaction:      existing,
				IdempotentReplay: true,
			}
			return nil
		}

		if wallet.Balance < amount {
			return domain.ErrInsufficientFunds
		}

		newBalance := wallet.Balance - amount
		oid := orderID
		txn := domain.Transaction{
			ID:           uuid.NewString(),
			WalletID:     walletID,
			Type:         domain.TransactionTypeDeduct,
			Amount:       amount,
			BalanceAfter: newBalance,
			OrderID:      &oid,
			CreatedAt:    time.Now().UTC(),
		}

		// Insert before mutating the balance: if this hits the safety-net
		// unique-constraint replay, the balance is never touched for this
		// attempt, so a duplicate can never be double-deducted even in that
		// (structurally unreachable) path.
		inserted, safetyNetReplay, err := locked.InsertTransaction(ctx, txn)
		if err != nil {
			return err
		}
		if safetyNetReplay {
			result = DeductResult{
				WalletID:         walletID,
				Balance:          wallet.Balance,
				Transaction:      inserted,
				IdempotentReplay: true,
			}
			return nil
		}

		if err := locked.UpdateBalance(ctx, newBalance); err != nil {
			return err
		}

		result = DeductResult{
			WalletID:         walletID,
			Balance:          newBalance,
			Transaction:      inserted,
			IdempotentReplay: false,
		}
		return nil
	})
	if err != nil {
		return DeductResult{}, err
	}
	return result, nil
}

func (s *WalletService) GetBalance(ctx context.Context, walletID string) (domain.Wallet, error) {
	return s.repo.GetWallet(ctx, walletID)
}

func (s *WalletService) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error) {
	if _, err := s.repo.GetWallet(ctx, walletID); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	return s.repo.ListTransactions(ctx, walletID, limit, offset)
}
