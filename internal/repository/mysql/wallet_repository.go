package mysql

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"wallet/internal/domain"
	"wallet/internal/service"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(dsn string) (*Repository, error) {
	// IgnoreRecordNotFoundError: the idempotency check in the deduct flow
	// looks up a transaction by order_id and is expected to miss on every
	// first-time call — that's the normal path, not an error worth logging.
	gormLogger := logger.New(
		log.New(os.Stdout, "", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}
	return &Repository{db: db}, nil
}

// AutoMigrate creates the wallets/transactions tables and their
// indexes/constraints from the struct tags in models.go if they don't
// already exist. Safe to call on every startup.
func (r *Repository) AutoMigrate(ctx context.Context) error {
	db := r.db.WithContext(ctx)
	if err := db.AutoMigrate(&walletRecord{}, &transactionRecord{}); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	// AutoMigrate only creates foreign keys for declared struct associations;
	// transactionRecord.WalletID is a plain scalar (deliberately, to avoid
	// GORM's implicit preload/join behavior on an association field), so the
	// FK to wallets(id) is added here instead, guarded by an existence check
	// so it's safe to run on every startup.
	if err := r.ensureWalletForeignKey(db); err != nil {
		return fmt.Errorf("ensure wallet foreign key: %w", err)
	}
	return nil
}

func (r *Repository) ensureWalletForeignKey(db *gorm.DB) error {
	var count int64
	err := db.Raw(`
		SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
		WHERE CONSTRAINT_SCHEMA = DATABASE()
		AND TABLE_NAME = 'transactions'
		AND CONSTRAINT_NAME = 'fk_transactions_wallet'
	`).Scan(&count).Error
	if err != nil {
		return fmt.Errorf("check existing foreign key: %w", err)
	}
	if count > 0 {
		return nil
	}
	err = db.Exec(`
		ALTER TABLE transactions
		ADD CONSTRAINT fk_transactions_wallet
		FOREIGN KEY (wallet_id) REFERENCES wallets(id)
	`).Error
	if err != nil {
		return fmt.Errorf("add foreign key: %w", err)
	}
	return nil
}

func (r *Repository) CreateWallet(ctx context.Context, wallet domain.Wallet) (domain.Wallet, error) {
	rec := fromDomainWallet(wallet)
	if err := r.db.WithContext(ctx).Create(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return domain.Wallet{}, domain.ErrDuplicateWallet
		}
		return domain.Wallet{}, fmt.Errorf("create wallet: %w", err)
	}
	return toDomainWallet(rec), nil
}

func (r *Repository) GetWallet(ctx context.Context, id string) (domain.Wallet, error) {
	var rec walletRecord
	if err := r.db.WithContext(ctx).First(&rec, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Wallet{}, domain.ErrWalletNotFound
		}
		return domain.Wallet{}, fmt.Errorf("get wallet: %w", err)
	}
	return toDomainWallet(rec), nil
}

func (r *Repository) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error) {
	var recs []transactionRecord
	if err := r.db.WithContext(ctx).
		Where("wallet_id = ?", walletID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&recs).Error; err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}

	result := make([]domain.Transaction, 0, len(recs))
	for _, rec := range recs {
		result = append(result, toDomainTransaction(rec))
	}
	return result, nil
}

func (r *Repository) WithWalletLock(ctx context.Context, walletID string, fn func(ctx context.Context, locked service.LockedWallet) error) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rec walletRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rec, "id = ?", walletID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrWalletNotFound
			}
			return fmt.Errorf("lock wallet: %w", err)
		}

		locked := &lockedWallet{tx: tx, wallet: toDomainWallet(rec)}
		return fn(ctx, locked)
	})
	if err != nil {
		return err
	}
	return nil
}

// lockedWallet implements service.LockedWallet, scoped to one row-locked
// transaction. Every method here runs against tx, never against r.db
// directly, so all operations share the same lock and commit/rollback
// atomically with the surrounding transaction.
type lockedWallet struct {
	tx     *gorm.DB
	wallet domain.Wallet
}

func (l *lockedWallet) Wallet() domain.Wallet { return l.wallet }

func (l *lockedWallet) FindTransactionByOrderID(ctx context.Context, orderID string, txnType domain.TransactionType) (domain.Transaction, bool, error) {
	var rec transactionRecord
	err := l.tx.WithContext(ctx).Where("wallet_id = ? AND order_id = ? AND type = ?", l.wallet.ID, orderID, string(txnType)).First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Transaction{}, false, nil
		}
		return domain.Transaction{}, false, fmt.Errorf("find transaction by order id: %w", err)
	}
	return toDomainTransaction(rec), true, nil
}

func (l *lockedWallet) InsertTransaction(ctx context.Context, txn domain.Transaction) (domain.Transaction, bool, error) {
	rec := fromDomainTransaction(txn)
	err := l.tx.WithContext(ctx).Create(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) && txn.OrderID != nil {
			var existing transactionRecord
			findErr := l.tx.WithContext(ctx).
				Where("wallet_id = ? AND order_id = ? AND type = ?", l.wallet.ID, *txn.OrderID, string(txn.Type)).
				First(&existing).Error
			if findErr != nil {
				return domain.Transaction{}, false, fmt.Errorf("re-fetch after duplicate transaction insert: %w", findErr)
			}
			return toDomainTransaction(existing), true, nil
		}
		return domain.Transaction{}, false, fmt.Errorf("insert transaction: %w", err)
	}
	return toDomainTransaction(rec), false, nil
}

func (l *lockedWallet) UpdateBalance(ctx context.Context, newBalance int64) error {
	err := l.tx.WithContext(ctx).
		Model(&walletRecord{}).
		Where("id = ?", l.wallet.ID).
		Update("balance", newBalance).Error
	if err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	l.wallet.Balance = newBalance
	return nil
}

func toDomainWallet(rec walletRecord) domain.Wallet {
	return domain.Wallet{
		ID:         rec.ID,
		CustomerID: rec.CustomerID,
		Balance:    rec.Balance,
		CreatedAt:  rec.CreatedAt,
		UpdatedAt:  rec.UpdatedAt,
	}
}

func fromDomainWallet(w domain.Wallet) walletRecord {
	return walletRecord{
		ID:         w.ID,
		CustomerID: w.CustomerID,
		Balance:    w.Balance,
		CreatedAt:  w.CreatedAt,
		UpdatedAt:  w.UpdatedAt,
	}
}

func toDomainTransaction(rec transactionRecord) domain.Transaction {
	return domain.Transaction{
		ID:           rec.ID,
		WalletID:     rec.WalletID,
		Type:         domain.TransactionType(rec.Type),
		Amount:       rec.Amount,
		BalanceAfter: rec.BalanceAfter,
		OrderID:      rec.OrderID,
		CreatedAt:    rec.CreatedAt,
	}
}

func fromDomainTransaction(t domain.Transaction) transactionRecord {
	return transactionRecord{
		ID:           t.ID,
		WalletID:     t.WalletID,
		Type:         string(t.Type),
		Amount:       t.Amount,
		BalanceAfter: t.BalanceAfter,
		OrderID:      t.OrderID,
		CreatedAt:    t.CreatedAt,
	}
}
