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

// uq_transactions_wallet_order is scoped to (wallet_id, order_id, type), not just
// (wallet_id, order_id): the priority tags keep wallet_id+order_id as the leading
// columns (so the FindTransactionByOrderID lookup, which filters on exactly those
// two, still hits a leftmost-prefix match), with type appended so a future
// order-linked type — e.g. a refund reversing a deduct for the same order — can
// coexist as its own row instead of colliding with the original deduct's row.
// If/when that lands, FindTransactionByOrderID-style lookups must start filtering
// by type too, or a lookup for one type could match a different type's row.
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
