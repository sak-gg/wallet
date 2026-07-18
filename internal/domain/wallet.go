package domain

import "time"

type Wallet struct {
	ID         string
	CustomerID string
	Balance    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type TransactionType string

const (
	TransactionTypeTopup  TransactionType = "topup"
	TransactionTypeDeduct TransactionType = "deduct"
)

type Transaction struct {
	ID           string
	WalletID     string
	Type         TransactionType
	Amount       int64
	BalanceAfter int64
	OrderID      *string
	CreatedAt    time.Time
}
