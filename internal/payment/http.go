package payment

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// CodeBankAccountRequired answers a top-up or withdrawal without a linked bank account.
const CodeBankAccountRequired = "BANK_ACCOUNT_REQUIRED"

// Handler serves the payment endpoints.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler returns the payment handler. log may be nil.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, log: log}
}

// Routes mounts the endpoints; requireAuth is the identity middleware.
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/topups", requireAuth(http.HandlerFunc(h.topUp)))
}

type amountRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

// acceptedBody is the 202 answer of a top-up or withdrawal request (PRD-002 §6).
type acceptedBody struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Type          string    `json:"type"`
	Status        string    `json:"status"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
}

// topUp handles POST /v1/topups. Checks 1–5 (token, body, key, amount, currency) are answered
// here and not stored for the key; checks 6–9 run in RequestTopUp.
func (h *Handler) topUp(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpserver.UserID(r.Context())
	if !ok {
		h.internalError(w, r, errors.New("request without authenticated user"))
		return
	}
	var req amountRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil { // 2
		httpserver.WriteDecodeError(w, err)
		return
	}
	key, err := idempotency.KeyFromRequest(r) // 3
	if err != nil {
		idempotency.WriteError(w, err)
		return
	}
	amount, ok := validAmount(w, req) // 4, 5
	if !ok {
		return
	}
	hash, err := idempotency.RequestHash(req)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	in := TopUpInput{UserID: userID, Amount: amount,
		Idempotency: idempotency.Request{UserID: userID, Key: key, Method: r.Method, Path: r.URL.Path, Hash: hash}}

	resp, err := h.svc.RequestTopUp(r.Context(), in, func(txn transaction.Transaction, err error) (int, any) {
		return render(w, txn, err)
	})
	h.write(w, r, resp, err)
}

// validAmount does checks 4 and 5 and writes their error.
func validAmount(w http.ResponseWriter, req amountRequest) (money.Money, bool) {
	amount, err := money.NewPositive(req.AmountMinor, money.EGP)
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, wallet.CodeInvalidAmount, "amount_minor must be a positive integer number of piasters.")
		return money.Money{}, false
	}
	if req.Currency != string(money.EGP) {
		httpserver.WriteError(w, http.StatusBadRequest, wallet.CodeUnsupportedCurrency, "Only EGP is supported.")
		return money.Money{}, false
	}
	return amount, true
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request, resp Response, err error) {
	if err != nil {
		if !idempotency.WriteError(w, err) && !httpserver.WriteTryAgain(w, err) {
			h.internalError(w, r, err)
		}
		return
	}
	if resp.Replay != nil {
		idempotency.WriteReplay(w, *resp.Replay)
		return
	}
	httpserver.WriteJSON(w, resp.Status, resp.Body)
}

// businessError is how one business error is answered; the same table decides how errors
// are rendered and stored for the key.
type businessError struct {
	err     error
	status  int
	code    string
	message string
}

var businessErrors = []businessError{
	{wallet.ErrFrozen, http.StatusForbidden, wallet.CodeWalletFrozen, "Your wallet is frozen."},
	{ErrBankAccountRequired, http.StatusUnprocessableEntity, CodeBankAccountRequired, "Link a bank account first."},
	{wallet.ErrAmountOutOfRange, http.StatusUnprocessableEntity, wallet.CodeAmountOutOfRange, "The amount is outside the allowed range."},
	{wallet.ErrWalletLimitExceeded, http.StatusUnprocessableEntity, wallet.CodeWalletLimitExceeded, "This would take your wallet above its maximum balance."},
}

// render maps the outcome of a request to its response: 202 with the PENDING transaction,
// or a business error. Anything else would be a bug and is answered with 500.
func render(w http.ResponseWriter, txn transaction.Transaction, err error) (int, any) {
	if err == nil {
		return http.StatusAccepted, acceptedBody{
			TransactionID: txn.ID, Type: string(txn.Type), Status: string(txn.Status),
			AmountMinor: txn.Amount.Amount(), Currency: string(txn.Amount.Currency()), CreatedAt: txn.CreatedAt.UTC(),
		}
	}
	for _, be := range businessErrors {
		if errors.Is(err, be.err) {
			return be.status, httpserver.ErrorBody(w, be.code, be.message)
		}
	}
	return http.StatusInternalServerError, httpserver.ErrorBody(w, httpserver.CodeInternal, "Internal server error.")
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "payment: request failed", slog.Any("error", err))
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
}
