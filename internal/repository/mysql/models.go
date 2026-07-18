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

type transactionRecord struct {
	ID           string    `gorm:"type:char(36);primaryKey"`
	WalletID     string    `gorm:"type:char(36);not null;index:idx_transactions_wallet_created;uniqueIndex:uq_transactions_wallet_order"`
	Type         string    `gorm:"type:varchar(16);not null;check:type in ('topup','deduct')"`
	Amount       int64     `gorm:"not null;check:amount > 0"`
	BalanceAfter int64     `gorm:"not null;check:balance_after >= 0"`
	OrderID      *string   `gorm:"type:varchar(255);uniqueIndex:uq_transactions_wallet_order"`
	CreatedAt    time.Time `gorm:"index:idx_transactions_wallet_created"`
}

func (transactionRecord) TableName() string { return "transactions" }
