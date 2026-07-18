package domain

import "errors"

var (
	ErrWalletNotFound    = errors.New("wallet not found")
	ErrDuplicateWallet   = errors.New("wallet already exists for customer")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidAmount     = errors.New("amount must be a positive integer")
	ErrInvalidCustomerID = errors.New("customer_id is required")
	ErrInvalidOrderID    = errors.New("order_id is required")
)
