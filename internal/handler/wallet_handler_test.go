package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wallet/internal/domain"
	"wallet/internal/handler"
	"wallet/internal/service"
)

type fakeService struct {
	createWalletFn     func(ctx context.Context, customerID string) (domain.Wallet, error)
	topUpFn            func(ctx context.Context, walletID string, amount int64, orderID string) (service.TopUpResult, error)
	deductFn           func(ctx context.Context, walletID string, amount int64, orderID string) (service.DeductResult, error)
	getBalanceFn       func(ctx context.Context, walletID string) (domain.Wallet, error)
	listTransactionsFn func(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error)
}

func (f *fakeService) CreateWallet(ctx context.Context, customerID string) (domain.Wallet, error) {
	return f.createWalletFn(ctx, customerID)
}

func (f *fakeService) TopUp(ctx context.Context, walletID string, amount int64, orderID string) (service.TopUpResult, error) {
	return f.topUpFn(ctx, walletID, amount, orderID)
}

func (f *fakeService) Deduct(ctx context.Context, walletID string, amount int64, orderID string) (service.DeductResult, error) {
	return f.deductFn(ctx, walletID, amount, orderID)
}

func (f *fakeService) GetBalance(ctx context.Context, walletID string) (domain.Wallet, error) {
	return f.getBalanceFn(ctx, walletID)
}

func (f *fakeService) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error) {
	return f.listTransactionsFn(ctx, walletID, limit, offset)
}

func init() {
	gin.SetMode(gin.TestMode)
}

const validWalletID = "11111111-1111-1111-1111-111111111111"

func doRequest(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, rec.Body.String())
	}
	return body.Error.Code
}

func TestCreateWallet_Success(t *testing.T) {
	fake := &fakeService{
		createWalletFn: func(ctx context.Context, customerID string) (domain.Wallet, error) {
			return domain.Wallet{ID: validWalletID, CustomerID: customerID, Balance: 0}, nil
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": "cust-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateWallet_DuplicateWallet(t *testing.T) {
	fake := &fakeService{
		createWalletFn: func(ctx context.Context, customerID string) (domain.Wallet, error) {
			return domain.Wallet{}, domain.ErrDuplicateWallet
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets", map[string]string{"customer_id": "cust-1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "DUPLICATE_WALLET" {
		t.Fatalf("expected DUPLICATE_WALLET, got %q", code)
	}
}

func TestCreateWallet_MalformedJSON(t *testing.T) {
	fake := &fakeService{}
	r := handler.NewRouter(handler.NewHandler(fake))

	req := httptest.NewRequest(http.MethodPost, "/wallets", bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTopUp_WalletNotFound(t *testing.T) {
	fake := &fakeService{
		topUpFn: func(ctx context.Context, walletID string, amount int64, orderID string) (service.TopUpResult, error) {
			return service.TopUpResult{}, domain.ErrWalletNotFound
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets/"+validWalletID+"/topup", map[string]any{"amount": 500, "order_id": "topup-1"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "WALLET_NOT_FOUND" {
		t.Fatalf("expected WALLET_NOT_FOUND, got %q", code)
	}
}

func TestTopUp_InvalidWalletIDInPath(t *testing.T) {
	fake := &fakeService{}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets/not-a-uuid/topup", map[string]int64{"amount": 500})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTopUp_InvalidAmount(t *testing.T) {
	fake := &fakeService{
		topUpFn: func(ctx context.Context, walletID string, amount int64, orderID string) (service.TopUpResult, error) {
			return service.TopUpResult{}, domain.ErrInvalidAmount
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets/"+validWalletID+"/topup", map[string]any{"amount": 0, "order_id": "topup-1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %q", code)
	}
}

func TestTopUp_FloatAmountRejected(t *testing.T) {
	fake := &fakeService{
		topUpFn: func(ctx context.Context, walletID string, amount int64, orderID string) (service.TopUpResult, error) {
			t.Fatalf("service should not be called for a malformed body")
			return service.TopUpResult{}, nil
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	req := httptest.NewRequest(http.MethodPost, "/wallets/"+validWalletID+"/topup", bytes.NewReader([]byte(`{"amount": 100.5}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for float amount, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeduct_InsufficientFunds(t *testing.T) {
	fake := &fakeService{
		deductFn: func(ctx context.Context, walletID string, amount int64, orderID string) (service.DeductResult, error) {
			return service.DeductResult{}, domain.ErrInsufficientFunds
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets/"+validWalletID+"/deduct", map[string]any{"amount": 100, "order_id": "order-1"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec); code != "INSUFFICIENT_FUNDS" {
		t.Fatalf("expected INSUFFICIENT_FUNDS, got %q", code)
	}
}

func TestDeduct_Success(t *testing.T) {
	fake := &fakeService{
		deductFn: func(ctx context.Context, walletID string, amount int64, orderID string) (service.DeductResult, error) {
			oid := orderID
			return service.DeductResult{
				WalletID: walletID,
				Balance:  400,
				Transaction: domain.Transaction{
					ID:           "txn-1",
					WalletID:     walletID,
					Type:         domain.TransactionTypeDeduct,
					Amount:       amount,
					BalanceAfter: 400,
					OrderID:      &oid,
					CreatedAt:    time.Now().UTC(),
				},
				IdempotentReplay: false,
			}, nil
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodPost, "/wallets/"+validWalletID+"/deduct", map[string]any{"amount": 100, "order_id": "order-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetBalance_WalletNotFound(t *testing.T) {
	fake := &fakeService{
		getBalanceFn: func(ctx context.Context, walletID string) (domain.Wallet, error) {
			return domain.Wallet{}, domain.ErrWalletNotFound
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodGet, "/wallets/"+validWalletID+"/balance", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTransactions_InvalidLimit(t *testing.T) {
	fake := &fakeService{}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodGet, "/wallets/"+validWalletID+"/transactions?limit=-1", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListTransactions_Success(t *testing.T) {
	fake := &fakeService{
		listTransactionsFn: func(ctx context.Context, walletID string, limit, offset int) ([]domain.Transaction, error) {
			if limit != 50 || offset != 0 {
				t.Fatalf("expected default limit=50 offset=0, got limit=%d offset=%d", limit, offset)
			}
			return []domain.Transaction{}, nil
		},
	}
	r := handler.NewRouter(handler.NewHandler(fake))

	rec := doRequest(t, r, http.MethodGet, "/wallets/"+validWalletID+"/transactions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
