package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"wallet/internal/domain"
	"wallet/internal/service"
)

// WalletService is the business-logic dependency of Handler, declared here
// at the consumer. Satisfied by *service.WalletService.
type WalletService interface {
	CreateWallet(ctx context.Context, customerID string) (domain.Wallet, error)
	TopUp(ctx context.Context, walletID string, amount int64) (service.TopUpResult, error)
	Deduct(ctx context.Context, walletID string, amount int64, orderID string) (service.DeductResult, error)
	GetBalance(ctx context.Context, walletID string) (domain.Wallet, error)
	ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error)
}

type Handler struct {
	service WalletService
}

func NewHandler(service WalletService) *Handler {
	return &Handler{service: service}
}

type createWalletRequest struct {
	CustomerID string `json:"customer_id"`
}

type walletResponse struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	Balance    int64     `json:"balance"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type topupRequest struct {
	Amount int64 `json:"amount"`
}

type deductRequest struct {
	Amount  int64  `json:"amount"`
	OrderID string `json:"order_id"`
}

type transactionResponse struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Amount       int64     `json:"amount"`
	BalanceAfter int64     `json:"balance_after"`
	OrderID      *string   `json:"order_id"`
	CreatedAt    time.Time `json:"created_at"`
}

type topupResponse struct {
	WalletID    string              `json:"wallet_id"`
	Balance     int64               `json:"balance"`
	Transaction transactionResponse `json:"transaction"`
}

type deductResponse struct {
	WalletID         string              `json:"wallet_id"`
	Balance          int64               `json:"balance"`
	Transaction      transactionResponse `json:"transaction"`
	IdempotentReplay bool                `json:"idempotent_replay"`
}

type balanceResponse struct {
	WalletID   string `json:"wallet_id"`
	CustomerID string `json:"customer_id"`
	Balance    int64  `json:"balance"`
}

type transactionsResponse struct {
	WalletID     string                `json:"wallet_id"`
	Transactions []transactionResponse `json:"transactions"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *Handler) CreateWallet(c *gin.Context) {
	var req createWalletRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	wallet, err := h.service.CreateWallet(c.Request.Context(), req.CustomerID)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toWalletResponse(wallet))
}

func (h *Handler) TopUp(c *gin.Context) {
	walletID, ok := parseWalletID(c)
	if !ok {
		return
	}

	var req topupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	result, err := h.service.TopUp(c.Request.Context(), walletID, req.Amount)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, topupResponse{
		WalletID:    result.WalletID,
		Balance:     result.Balance,
		Transaction: toTransactionResponse(result.Transaction),
	})
}

func (h *Handler) Deduct(c *gin.Context) {
	walletID, ok := parseWalletID(c)
	if !ok {
		return
	}

	var req deductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	result, err := h.service.Deduct(c.Request.Context(), walletID, req.Amount, req.OrderID)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, deductResponse{
		WalletID:         result.WalletID,
		Balance:          result.Balance,
		Transaction:      toTransactionResponse(result.Transaction),
		IdempotentReplay: result.IdempotentReplay,
	})
}

func (h *Handler) GetBalance(c *gin.Context) {
	walletID, ok := parseWalletID(c)
	if !ok {
		return
	}

	wallet, err := h.service.GetBalance(c.Request.Context(), walletID)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, balanceResponse{
		WalletID:   wallet.ID,
		CustomerID: wallet.CustomerID,
		Balance:    wallet.Balance,
	})
}

func (h *Handler) ListTransactions(c *gin.Context) {
	walletID, ok := parseWalletID(c)
	if !ok {
		return
	}

	limit, ok := parseNonNegativeQueryParam(c, "limit", 50)
	if !ok {
		return
	}
	offset, ok := parseNonNegativeQueryParam(c, "offset", 0)
	if !ok {
		return
	}

	txns, err := h.service.ListTransactions(c.Request.Context(), walletID, limit, offset)
	if err != nil {
		writeServiceError(c, err)
		return
	}

	responses := make([]transactionResponse, 0, len(txns))
	for _, t := range txns {
		responses = append(responses, toTransactionResponse(t))
	}

	c.JSON(http.StatusOK, transactionsResponse{WalletID: walletID, Transactions: responses})
}

// parseWalletID validates the :id path param is a syntactically valid UUID
// before it ever reaches the service, so a malformed ID is a 400 distinct
// from a well-formed-but-absent one (404, from the service/repository).
func parseWalletID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid wallet id")
		return "", false
	}
	return id, true
}

func parseNonNegativeQueryParam(c *gin.Context, name string, def int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return def, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid "+name)
		return 0, false
	}
	return value, true
}

func toWalletResponse(w domain.Wallet) walletResponse {
	return walletResponse{
		ID:         w.ID,
		CustomerID: w.CustomerID,
		Balance:    w.Balance,
		CreatedAt:  w.CreatedAt,
		UpdatedAt:  w.UpdatedAt,
	}
}

func toTransactionResponse(t domain.Transaction) transactionResponse {
	return transactionResponse{
		ID:           t.ID,
		Type:         string(t.Type),
		Amount:       t.Amount,
		BalanceAfter: t.BalanceAfter,
		OrderID:      t.OrderID,
		CreatedAt:    t.CreatedAt,
	}
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrWalletNotFound):
		writeError(c, http.StatusNotFound, "WALLET_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrDuplicateWallet):
		writeError(c, http.StatusConflict, "DUPLICATE_WALLET", err.Error())
	case errors.Is(err, domain.ErrInsufficientFunds):
		writeError(c, http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", err.Error())
	case errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidCustomerID),
		errors.Is(err, domain.ErrInvalidOrderID):
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	default:
		log.Printf("internal error: %v", err)
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
