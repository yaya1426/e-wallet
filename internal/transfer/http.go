package transfer

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/idempotency"
	"github.com/yaya1426/e-wallet/internal/identity"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/platform/money"
	"github.com/yaya1426/e-wallet/internal/wallet"
)

// Error codes of POST /v1/transfers (T008) besides the shared wallet and identity codes.
const (
	CodeInvalidNote            = "INVALID_NOTE"
	CodeRecipientNotFound      = "RECIPIENT_NOT_FOUND"
	CodeSelfTransferNotAllowed = "SELF_TRANSFER_NOT_ALLOWED"
)

// Handler serves POST /v1/transfers.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler returns the transfer handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Routes mounts the endpoint; requireAuth is the identity middleware.
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/transfers", requireAuth(http.HandlerFunc(h.send)))
}

type sendRequest struct {
	RecipientPhone string `json:"recipient_phone"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Note           string `json:"note"`
}

type recipientBody struct {
	Phone      string `json:"phone"`
	MaskedName string `json:"masked_name"`
}

type transferBody struct {
	TransactionID     uuid.UUID     `json:"transaction_id"`
	Type              string        `json:"type"`
	Status            string        `json:"status"`
	AmountMinor       int64         `json:"amount_minor"`
	Currency          string        `json:"currency"`
	Recipient         recipientBody `json:"recipient"`
	Note              *string       `json:"note"`
	BalanceAfterMinor int64         `json:"balance_after_minor"`
	CreatedAt         time.Time     `json:"created_at"`
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) {
	userID, ok := httpserver.UserID(r.Context())
	if !ok {
		h.log.ErrorContext(r.Context(), "transfer: request without authenticated user")
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
		return
	}
	var req sendRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil { // 2
		httpserver.WriteDecodeError(w, err)
		return
	}
	key, err := idempotency.KeyFromRequest(r) // 3
	if err != nil {
		idempotency.WriteError(w, err)
		return
	}
	in, err := validate(userID, req) // 4–7
	if err != nil {
		writeValidationError(w, err)
		return
	}
	// The hash covers the normalized request, so " lunch " and "lunch" are the same request.
	req.Note = in.Note
	hash, err := idempotency.RequestHash(req)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	in.Idempotency = idempotency.Request{UserID: userID, Key: key, Method: r.Method, Path: r.URL.Path, Hash: hash}

	resp, err := h.svc.Send(r.Context(), in, func(res Result, err error) (int, any) { return render(w, res, err) })
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

// validate does checks 4–7 and builds the input.
func validate(userID uuid.UUID, req sendRequest) (Input, error) {
	if req.AmountMinor <= 0 {
		return Input{}, ErrInvalidAmount
	}
	if req.Currency != string(money.EGP) {
		return Input{}, ErrUnsupportedCurrency
	}
	if !identity.ValidPhone(req.RecipientPhone) {
		return Input{}, ErrInvalidPhone
	}
	note, err := NormalizeNote(req.Note)
	if err != nil {
		return Input{}, err
	}
	amount, err := money.NewPositive(req.AmountMinor, money.EGP)
	if err != nil {
		return Input{}, ErrInvalidAmount
	}
	return Input{SenderID: userID, RecipientPhone: req.RecipientPhone, Amount: amount, Note: note}, nil
}

func writeValidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidAmount):
		httpserver.WriteError(w, http.StatusBadRequest, wallet.CodeInvalidAmount, "amount_minor must be a positive integer number of piasters.")
	case errors.Is(err, ErrUnsupportedCurrency):
		httpserver.WriteError(w, http.StatusBadRequest, wallet.CodeUnsupportedCurrency, "Only EGP is supported.")
	case errors.Is(err, ErrInvalidPhone):
		httpserver.WriteError(w, http.StatusBadRequest, identity.CodeInvalidPhone, "recipient_phone must be an Egyptian mobile number in E.164, e.g. +201001234567.")
	case errors.Is(err, ErrInvalidNote):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidNote, "Note must be at most 140 characters.")
	default:
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
	}
}

// businessError is how one business error (checks 8–14) is answered. The same table decides
// which errors are stored for the idempotency key (isBusinessError) and how they are
// rendered, so the two cannot drift apart.
type businessError struct {
	err     error
	status  int
	code    string
	message string
}

var businessErrors = []businessError{
	{wallet.ErrAmountOutOfRange, http.StatusUnprocessableEntity, wallet.CodeAmountOutOfRange, "The amount is outside the allowed range for transfers."},
	{ErrRecipientNotFound, http.StatusNotFound, CodeRecipientNotFound, "No user is registered with this phone number."},
	{ErrSelfTransfer, http.StatusUnprocessableEntity, CodeSelfTransferNotAllowed, "You cannot send money to yourself."},
	{wallet.ErrFrozen, http.StatusForbidden, wallet.CodeWalletFrozen, "Your wallet is frozen."},
	{wallet.ErrInsufficientFunds, http.StatusUnprocessableEntity, wallet.CodeInsufficientFunds, "Available balance is too low."},
	{wallet.ErrDailyLimitExceeded, http.StatusUnprocessableEntity, wallet.CodeDailyLimitExceeded, "This would exceed your daily outgoing limit."},
	{wallet.ErrRecipientLimitExceeded, http.StatusUnprocessableEntity, wallet.CodeRecipientLimitExceeded, "The recipient's wallet cannot hold this amount."},
}

func lookupBusinessError(err error) (businessError, bool) {
	for _, be := range businessErrors {
		if errors.Is(err, be.err) {
			return be, true
		}
	}
	return businessError{}, false
}

// isBusinessError reports whether err is one of checks 8–14, answered with a 4xx and stored
// for the idempotency key.
func isBusinessError(err error) bool {
	_, ok := lookupBusinessError(err)
	return ok
}

// render maps a transfer outcome to its response (T008 API table). Send calls it only for a
// success or a business error; anything else would be a bug and is answered with 500.
func render(w http.ResponseWriter, res Result, err error) (int, any) {
	if err == nil {
		var note *string
		if res.Transaction.Note != "" {
			note = &res.Transaction.Note
		}
		return http.StatusCreated, transferBody{
			TransactionID:     res.Transaction.ID,
			Type:              string(res.Transaction.Type),
			Status:            string(res.Transaction.Status),
			AmountMinor:       res.Transaction.Amount.Amount(),
			Currency:          string(res.Transaction.Amount.Currency()),
			Recipient:         recipientBody{Phone: res.RecipientPhone, MaskedName: res.RecipientMaskedName},
			Note:              note,
			BalanceAfterMinor: res.SenderBalanceAfter,
			CreatedAt:         res.Transaction.CreatedAt.UTC(),
		}
	}
	be, ok := lookupBusinessError(err)
	if !ok {
		return http.StatusInternalServerError, httpserver.ErrorBody(w, httpserver.CodeInternal, "Internal server error.")
	}
	return be.status, httpserver.ErrorBody(w, be.code, be.message)
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "transfer: request failed", slog.Any("error", err))
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
}
