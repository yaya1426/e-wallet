package discrepancy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// Error codes of the operator endpoints (T026).
const (
	CodeInvalidFilter       = "INVALID_FILTER"
	CodeInvalidLimit        = "INVALID_LIMIT"
	CodeInvalidCursor       = "INVALID_CURSOR"
	CodeDiscrepancyNotFound = "DISCREPANCY_NOT_FOUND"
	CodeAlreadyResolved     = "ALREADY_RESOLVED"
	CodeInvalidNote         = "INVALID_NOTE"
	maxPageSize             = 100
	defaultPageSize         = 50
	maxResolutionNoteRunes  = 500
)

// Handler serves the operator endpoints of discrepancies.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler returns the handler. log may be nil.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, log: log}
}

// InternalRoutes mounts the operator endpoints; requireKey is the internal key middleware.
func (h *Handler) InternalRoutes(mux *http.ServeMux, requireKey func(http.Handler) http.Handler) {
	mux.Handle("GET /internal/v1/discrepancies", requireKey(http.HandlerFunc(h.list)))
	mux.Handle("POST /internal/v1/discrepancies/{id}/resolve", requireKey(http.HandlerFunc(h.resolve)))
}

type itemBody struct {
	ID             uuid.UUID       `json:"id"`
	Kind           string          `json:"kind"`
	Severity       string          `json:"severity"`
	TransactionID  *uuid.UUID      `json:"transaction_id"`
	Details        json.RawMessage `json:"details"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	ResolvedAt     *time.Time      `json:"resolved_at"`
	ResolutionNote *string         `json:"resolution_note"`
}

func toBody(d Discrepancy) itemBody {
	b := itemBody{ID: d.ID, Kind: string(d.Kind), Severity: string(d.Severity), Details: d.Details,
		Status: string(d.Status), CreatedAt: d.CreatedAt.UTC()}
	if d.TransactionID != uuid.Nil {
		b.TransactionID = &d.TransactionID
	}
	if d.ResolvedAt != nil {
		t := d.ResolvedAt.UTC()
		b.ResolvedAt = &t
	}
	if d.ResolutionNote != "" {
		b.ResolutionNote = &d.ResolutionNote
	}
	return b
}

type pageBody struct {
	Items      []itemBody `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// list handles GET /internal/v1/discrepancies?status=&kind=&limit=&cursor=.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filter{Status: Status(q.Get("status")), Kind: Kind(q.Get("kind")), Limit: defaultPageSize}
	if f.Status != "" && f.Status != StatusOpen && f.Status != StatusResolved || f.Kind != "" && !f.Kind.valid() {
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidFilter, "status must be OPEN or RESOLVED; kind must be a known discrepancy kind.")
		return
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPageSize {
			httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidLimit, "limit must be an integer from 1 to 100.")
			return
		}
		f.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		c, err := decodeCursor(raw)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidCursor, "cursor is invalid.")
			return
		}
		f.After = &c
	}
	pageSize := f.Limit
	f.Limit++ // one extra row tells whether there is a next page
	items, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	body := pageBody{Items: make([]itemBody, 0, min(len(items), pageSize))}
	for i, d := range items {
		if i == pageSize {
			last := items[pageSize-1]
			next := encodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			body.NextCursor = &next
			break
		}
		body.Items = append(body.Items, toBody(d))
	}
	httpserver.WriteJSON(w, http.StatusOK, body)
}

type resolveRequest struct {
	Note string `json:"note"`
}

// resolve handles POST /internal/v1/discrepancies/{id}/resolve.
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusNotFound, CodeDiscrepancyNotFound, "No discrepancy has this ID.")
		return
	}
	var req resolveRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" || utf8.RuneCountInString(note) > maxResolutionNoteRunes {
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidNote, "note must be 1 to 500 characters.")
		return
	}
	d, err := h.svc.Resolve(r.Context(), id, note)
	switch {
	case errors.Is(err, ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, CodeDiscrepancyNotFound, "No discrepancy has this ID.")
	case errors.Is(err, ErrAlreadyResolved):
		httpserver.WriteError(w, http.StatusConflict, CodeAlreadyResolved, "This discrepancy is already resolved.")
	case err != nil:
		h.internalError(w, r, err)
	default:
		h.log.InfoContext(r.Context(), "operator action", slog.String("action", "resolve_discrepancy"),
			slog.String("discrepancy_id", id.String()), slog.Int("note_length", utf8.RuneCountInString(note)))
		httpserver.WriteJSON(w, http.StatusOK, toBody(d))
	}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "discrepancy: request failed", slog.Any("error", err))
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
}

type cursorJSON struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

func encodeCursor(c Cursor) string {
	raw, _ := json.Marshal(cursorJSON{T: c.CreatedAt.UTC().Format(time.RFC3339Nano), ID: c.ID.String()}) // cannot fail
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	var c cursorJSON
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, err
	}
	t, err := time.Parse(time.RFC3339Nano, c.T)
	if err != nil {
		return Cursor{}, err
	}
	id, err := uuid.Parse(c.ID)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{CreatedAt: t, ID: id}, nil
}
