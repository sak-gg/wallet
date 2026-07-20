package mysql

import "time"

// walletRecord and transactionRecord are GORM-tagged persistence models,
// private to this package. They are deliberately not named Wallet/Transaction
// so they aren't confused with the framework-free domain.Wallet /
// domain.Transaction structs used everywhere outside this package.
type walletRecord struct {
	ID         string `gorm:"type:char(36);primaryKey"`
	CustomerID string `gorm:"type:varchar(255);not null;uniqueIndex:uq_wallets_customer_id"`
	Balance    int64  `gorm:"not null;default:0;check:balance >= 0"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (walletRecord) TableName() string { return "wallets" }

// order_id doubles as a generic external-reference column: for deduct rows
// it's the order being paid for, for topup rows it's the caller-supplied
// reference used for topup idempotency. uq_transactions_wallet_order is
// scoped to (wallet_id, order_id, type), not just (wallet_id, order_id), so
// a topup and a deduct (or a future refund type) can independently use the
// same order_id value without colliding — each type owns its own idempotency
// space. The priority tags keep wallet_id+order_id as the leading columns
// for index efficiency; FindTransactionByOrderID filters on all three
// (wallet_id, order_id, type), since filtering on just the first two would
// let one type's row satisfy a lookup meant for another type sharing the
// same order_id value.
type transactionRecord struct {
	ID           string    `gorm:"type:char(36);primaryKey"`
	WalletID     string    `gorm:"type:char(36);not null;index:idx_transactions_wallet_created;uniqueIndex:uq_transactions_wallet_order,priority:1"`
	Type         string    `gorm:"type:varchar(16);not null;check:type in ('topup','deduct');uniqueIndex:uq_transactions_wallet_order,priority:3"`
	Amount       int64     `gorm:"not null;check:amount > 0"`
	BalanceAfter int64     `gorm:"not null;check:balance_after >= 0"`
	OrderID      *string   `gorm:"type:varchar(255);uniqueIndex:uq_transactions_wallet_order,priority:2"`
	CreatedAt    time.Time `gorm:"index:idx_transactions_wallet_created"`
}

func (transactionRecord) TableName() string { return "transactions" }
