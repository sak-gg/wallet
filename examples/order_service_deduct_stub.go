// Stub showing how order-service integrates with the wallet service's
// POST /wallets/:id/deduct endpoint. This is illustrative code meant to live
// in order-service's own codebase — not runnable, not wired into this repo's
// build or server.
package orderservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// WalletAPIError carries the wallet service's error code so callers can
// branch on it (e.g. "INSUFFICIENT_FUNDS") instead of string-matching a
// message.
type WalletAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *WalletAPIError) Error() string {
	return fmt.Sprintf("wallet service: %s (%s)", e.Message, e.Code)
}

type Transaction struct {
	ID           string  `json:"id"`
	Amount       int64   `json:"amount"`
	BalanceAfter int64   `json:"balance_after"`
	OrderID      *string `json:"order_id"`
}

type DeductResult struct {
	WalletID         string      `json:"wallet_id"`
	Balance          int64       `json:"balance"`
	Transaction      Transaction `json:"transaction"`
	IdempotentReplay bool        `json:"idempotent_replay"`
}

// WalletClient is the HTTP client order-service keeps for calling the
// wallet service. baseURL points at the wallet service's real address in
// production (e.g. from service discovery/config).
type WalletClient struct {
	baseURL string
	http    *http.Client
}

func NewWalletClient(baseURL string, httpClient *http.Client) *WalletClient {
	return &WalletClient{baseURL: baseURL, http: httpClient}
}

// Deduct calls POST /wallets/:id/deduct to charge the wallet for an order.
// orderID is the idempotency key: retrying the same orderID after a
// timeout returns the original result with IdempotentReplay=true instead
// of deducting twice, so order-service can safely retry on failure.
func (c *WalletClient) Deduct(ctx context.Context, walletID string, amount int64, orderID string) (DeductResult, error) {
	reqBody, err := json.Marshal(map[string]any{"amount": amount, "order_id": orderID})
	if err != nil {
		return DeductResult{}, fmt.Errorf("marshal deduct request: %w", err)
	}

	url := c.baseURL + "/wallets/" + walletID + "/deduct"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return DeductResult{}, fmt.Errorf("build deduct request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return DeductResult{}, fmt.Errorf("call wallet service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return DeductResult{}, &WalletAPIError{StatusCode: resp.StatusCode, Code: errBody.Error.Code, Message: errBody.Error.Message}
	}

	var out DeductResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return DeductResult{}, fmt.Errorf("decode deduct response: %w", err)
	}
	return out, nil
}

// ChargeForOrder is order-service's call site: it charges the wallet when
// an order is placed, using the order ID as the idempotency key.
func ChargeForOrder(ctx context.Context, wallet *WalletClient, walletID, orderID string, amount int64) error {
	result, err := wallet.Deduct(ctx, walletID, amount, orderID)
	if err != nil {
		var apiErr *WalletAPIError
		if errors.As(err, &apiErr) && apiErr.Code == "INSUFFICIENT_FUNDS" {
			return fmt.Errorf("order %s rejected, insufficient wallet balance: %w", orderID, err)
		}
		return fmt.Errorf("charge wallet for order %s: %w", orderID, err)
	}

	if result.IdempotentReplay {
		// Retry of a request already applied (e.g. after a client timeout) — no double charge.
		return nil
	}
	// result.Balance is the wallet's new balance after this charge.
	return nil
}
