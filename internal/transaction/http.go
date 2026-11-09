package transaction

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// Error codes of the history endpoints (T010).
const (
	CodeInvalidLimit        = "INVALID_LIMIT"
	CodeInvalidCursor       = "INVALID_CURSOR"
	CodeTransactionNotFound = "TRANSACTION_NOT_FOUND"
)

// History page sizes (TDR-013).
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// ErrInvalidLimit means the limit query parameter is not an integer in 1–100.
var ErrInvalidLimit = errors.New("transaction: invalid limit")

// Direction is how a transaction moved the caller's money.
type Direction string

// Directions: OUT when the caller's wallet is the source, IN when it is the destination.
const (
	DirectionIn  Direction = "IN"
	DirectionOut Direction = "OUT"
)

// Handler serves the history endpoints.
type Handler struct {
	svc     *Service
	db      Querier
	wallets WalletOfUser
	parties Parties
	log     *slog.Logger
}

// NewHandler returns the history handler. db is the pool.
func NewHandler(svc *Service, db Querier, wallets WalletOfUser, parties Parties, log *slog.Logger) *Handler {
	return &Handler{svc: svc, db: db, wallets: wallets, parties: parties, log: log}
}

// Routes mounts the endpoints; requireAuth is the identity middleware.
func (h *Handler) Routes(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/me/transactions", requireAuth(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/transactions/{id}", requireAuth(http.HandlerFunc(h.get)))
}

type partyBody struct {
	Phone      string `json:"phone"`
	MaskedName string `json:"masked_name"`
}

type itemBody struct {
	ID             uuid.UUID  `json:"id"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	Direction      Direction  `json:"direction"`
	AmountMinor    int64      `json:"amount_minor"`
	Currency       string     `json:"currency"`
	Counterparty   *partyBody `json:"counterparty"`
	Note           *string    `json:"note"`
	FailureCode    *string    `json:"failure_code"`
	FailureMessage *string    `json:"failure_message"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

type pageBody struct {
	Items      []itemBody `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	walletID, ok := h.callerWallet(w, r)
	if !ok {
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidLimit, "limit must be an integer from 1 to 100.")
		return
	}
	var after *Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		c, err := DecodeCursor(raw)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidCursor, "cursor is not valid; use next_cursor from the previous page.")
			return
		}
		after = &c
	}
	page, err := h.svc.ListForWallet(r.Context(), h.db, walletID, after, limit)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items, err := h.items(r, walletID, page.Items)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	body := pageBody{Items: items}
	if page.Next != nil {
		next := page.Next.Encode()
		body.NextCursor = &next
	}
	httpserver.WriteJSON(w, http.StatusOK, body)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	walletID, ok := h.callerWallet(w, r)
	if !ok {
		return
	}
	notFound := func() {
		httpserver.WriteError(w, http.StatusNotFound, CodeTransactionNotFound, "Transaction not found.")
	}
	txnID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		notFound()
		return
	}
	txn, err := h.svc.GetVisible(r.Context(), h.db, txnID, walletID)
	if errors.Is(err, ErrNotFound) {
		notFound()
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items, err := h.items(r, walletID, []Transaction{txn})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, items[0])
}

// items renders transactions from the point of view of walletID, resolving the P2P
// counterparties of the whole page in one Parties call.
func (h *Handler) items(r *http.Request, walletID uuid.UUID, txns []Transaction) ([]itemBody, error) {
	var others []uuid.UUID
	for _, t := range txns {
		if t.Type == TypeP2P {
			others = append(others, counterpartyWallet(t, walletID))
		}
	}
	parties := map[uuid.UUID]Party{}
	if len(others) > 0 {
		var err error
		if parties, err = h.parties.ByWallet(r.Context(), others); err != nil {
			return nil, err
		}
	}
	items := make([]itemBody, 0, len(txns))
	for _, t := range txns {
		item := itemBody{
			ID:             t.ID,
			Type:           string(t.Type),
			Status:         string(t.Status),
			Direction:      DirectionIn,
			AmountMinor:    t.Amount.Amount(),
			Currency:       string(t.Amount.Currency()),
			Note:           optional(t.Note),
			FailureCode:    optional(t.FailureCode),
			FailureMessage: optional(t.FailureMessage),
			CreatedAt:      t.CreatedAt.UTC(),
			UpdatedAt:      t.UpdatedAt.UTC(),
			CompletedAt:    utc(t.CompletedAt),
		}
		if t.SourceWalletID == walletID {
			item.Direction = DirectionOut
		}
		if t.Type == TypeP2P {
			if p, ok := parties[counterpartyWallet(t, walletID)]; ok {
				item.Counterparty = &partyBody{Phone: p.Phone, MaskedName: p.MaskedName}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// callerWallet returns the authenticated user's wallet ID, or answers the request.
func (h *Handler) callerWallet(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := httpserver.UserID(r.Context())
	if !ok {
		h.internalError(w, r, errors.New("request without authenticated user"))
		return uuid.Nil, false
	}
	walletID, err := h.wallets.WalletID(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, err)
		return uuid.Nil, false
	}
	return walletID, true
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "transaction: request failed", slog.Any("error", err))
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
}

// parseLimit reads the limit query parameter: "" means the default, otherwise 1–100.
func parseLimit(s string) (int, error) {
	if s == "" {
		return defaultPageSize, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, ErrInvalidLimit
	}
	return n, nil
}

func counterpartyWallet(t Transaction, walletID uuid.UUID) uuid.UUID {
	if t.SourceWalletID == walletID {
		return t.DestinationWalletID
	}
	return t.SourceWalletID
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
