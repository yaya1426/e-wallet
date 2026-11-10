package wallet

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// Error codes shared by the money flows (P2P, top-up, withdrawal), catalogued in
// api/openapi/wallet.yaml.
const (
	CodeInvalidAmount          = "INVALID_AMOUNT"
	CodeUnsupportedCurrency    = "UNSUPPORTED_CURRENCY"
	CodeAmountOutOfRange       = "AMOUNT_OUT_OF_RANGE"
	CodeWalletFrozen           = "WALLET_FROZEN"
	CodeInsufficientFunds      = "INSUFFICIENT_FUNDS"
	CodeDailyLimitExceeded     = "DAILY_LIMIT_EXCEEDED"
	CodeRecipientLimitExceeded = "RECIPIENT_LIMIT_EXCEEDED"
	CodeWalletLimitExceeded    = "WALLET_LIMIT_EXCEEDED"
	CodeWalletNotFound         = "WALLET_NOT_FOUND"
)

// InternalRoutes mounts the operator endpoints (TDR-018 §5); requireKey is the internal key
// middleware.
func (h *Handler) InternalRoutes(mux *http.ServeMux, requireKey func(http.Handler) http.Handler) {
	mux.Handle("POST /internal/v1/wallets/{wallet_id}/freeze", requireKey(h.setStatus(StatusFrozen)))
	mux.Handle("POST /internal/v1/wallets/{wallet_id}/unfreeze", requireKey(h.setStatus(StatusActive)))
}

type statusResponse struct {
	WalletID uuid.UUID `json:"wallet_id"`
	Status   string    `json:"status"`
}

func (h *Handler) setStatus(status Status) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("wallet_id"))
		if err != nil {
			httpserver.WriteError(w, http.StatusNotFound, CodeWalletNotFound, "No wallet has this ID.")
			return
		}
		wal, err := h.svc.SetStatus(r.Context(), id, status)
		if errors.Is(err, ErrNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, CodeWalletNotFound, "No wallet has this ID.")
			return
		}
		if err != nil {
			h.log.ErrorContext(r.Context(), "wallet: set status failed", slog.Any("error", err))
			httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
			return
		}
		h.log.InfoContext(r.Context(), "operator action", slog.String("action", "set_wallet_status"),
			slog.String("wallet_id", id.String()), slog.String("status", string(status)))
		httpserver.WriteJSON(w, http.StatusOK, statusResponse{WalletID: wal.ID, Status: string(wal.Status)})
	})
}

// Handler serves the wallet endpoints.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler returns the wallet HTTP handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Routes mounts the wallet endpoints on mux. requireAuth wraps every endpoint that needs a
// signed-in user (the identity module's middleware).
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/me/wallet", requireAuth(http.HandlerFunc(h.getMyWallet)))
}

type walletResponse struct {
	WalletID              uuid.UUID `json:"wallet_id"`
	Currency              string    `json:"currency"`
	AvailableBalanceMinor int64     `json:"available_balance_minor"`
	Status                string    `json:"status"`
}

func (h *Handler) getMyWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpserver.UserID(r.Context())
	if !ok {
		// requireAuth always sets the user; reaching this is a wiring bug.
		h.log.ErrorContext(r.Context(), "wallet: request without authenticated user")
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
		return
	}
	view, err := h.svc.View(r.Context(), userID)
	if err != nil {
		// Every user gets a wallet in the same DB transaction (BR-5), so a missing wallet is
		// an inconsistency, not a client error.
		h.log.ErrorContext(r.Context(), "wallet: view failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, walletResponse{
		WalletID:              view.WalletID,
		Currency:              string(view.Currency),
		AvailableBalanceMinor: view.AvailableBalance.Amount(),
		Status:                string(view.Status),
	})
}
