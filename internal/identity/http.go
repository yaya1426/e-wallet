package identity

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/database"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
)

// Error codes of the identity endpoints (T004, catalogued in api/openapi/wallet.yaml).
const (
	CodeInvalidPhone           = "INVALID_PHONE"
	CodeInvalidPassword        = "INVALID_PASSWORD"
	CodeInvalidName            = "INVALID_NAME"
	CodePhoneAlreadyRegistered = "PHONE_ALREADY_REGISTERED"
	CodeInvalidCredentials     = "INVALID_CREDENTIALS" //nolint:gosec // G101 false positive: an error code, not a credential
	CodeUserDisabled           = "USER_DISABLED"
	CodeTooManyRequests        = "TOO_MANY_REQUESTS"
	CodeUnauthenticated        = "UNAUTHENTICATED"
	CodeInvalidIBAN            = "INVALID_IBAN"
	CodeInvalidHolderName      = "INVALID_HOLDER_NAME"
	CodeBankAccountNotFound    = "BANK_ACCOUNT_NOT_FOUND"
	// Shared with POST /v1/transfers (T008), which imports identity.
	CodeRecipientNotFound      = "RECIPIENT_NOT_FOUND"
	CodeSelfTransferNotAllowed = "SELF_TRANSFER_NOT_ALLOWED"
)

// Handler serves the auth endpoints and provides the auth middleware.
type Handler struct {
	svc        *Service
	limiter    *LoginLimiter
	registerRL *RegisterLimiter
	log        *slog.Logger
}

// NewHandler returns the identity HTTP handler.
func NewHandler(svc *Service, limiter *LoginLimiter, register *RegisterLimiter, log *slog.Logger) *Handler {
	return &Handler{svc: svc, limiter: limiter, registerRL: register, log: log}
}

// Routes mounts the auth and bank-account endpoints on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", h.register)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.Handle("PUT /v1/me/bank-account", h.RequireAuth(http.HandlerFunc(h.putBankAccount)))
	mux.Handle("GET /v1/me/bank-account", h.RequireAuth(http.HandlerFunc(h.getBankAccount)))
	mux.Handle("GET /v1/recipients", h.RequireAuth(http.HandlerFunc(h.previewRecipient)))
}

type recipientResponse struct {
	Phone      string `json:"phone"`
	MaskedName string `json:"masked_name"`
}

// previewRecipient answers GET /v1/recipients?phone=… with the masked name only (T009).
func (h *Handler) previewRecipient(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requestUser(w, r)
	if !ok {
		return
	}
	recipient, err := h.svc.PreviewRecipient(r.Context(), userID, r.URL.Query().Get("phone"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, recipientResponse(recipient))
}

type registerRequest struct {
	Phone    string `json:"phone"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
}

type registerResponse struct {
	UserID    uuid.UUID `json:"user_id"`
	Phone     string    `json:"phone"`
	FullName  string    `json:"full_name"`
	WalletID  uuid.UUID `json:"wallet_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if !h.registerRL.Allow(httpserver.ClientIP(r)) {
		httpserver.WriteError(w, http.StatusTooManyRequests, CodeTooManyRequests, "Too many registrations from this network. Try again later.")
		return
	}
	reg, err := h.svc.Register(r.Context(), RegisterInput(req))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, registerResponse{
		UserID:    reg.User.ID,
		Phone:     reg.User.Phone,
		FullName:  reg.User.FullName,
		WalletID:  reg.WalletID,
		CreatedAt: reg.User.CreatedAt.UTC(),
	})
}

type loginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"` // seconds
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if !h.limiter.Allow(req.Phone, httpserver.ClientIP(r)) {
		httpserver.WriteError(w, http.StatusTooManyRequests, CodeTooManyRequests, "Too many login attempts. Try again in a minute.")
		return
	}
	token, err := h.svc.Login(r.Context(), req.Phone, req.Password)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, loginResponse{
		AccessToken: token.Token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(token.ExpiresIn / time.Second),
	})
}

type bankAccountRequest struct {
	IBAN       string `json:"iban"`
	HolderName string `json:"holder_name"`
}

type bankAccountResponse struct {
	IBAN       string    `json:"iban"`
	HolderName string    `json:"holder_name"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (h *Handler) putBankAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requestUser(w, r)
	if !ok {
		return
	}
	var req bankAccountRequest
	if err := httpserver.DecodeJSON(w, r, &req); err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	account, err := h.svc.LinkBankAccount(r.Context(), userID, req.IBAN, req.HolderName)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeBankAccount(w, account)
}

func (h *Handler) getBankAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requestUser(w, r)
	if !ok {
		return
	}
	account, err := h.svc.LinkedBankAccount(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeBankAccount(w, account)
}

func writeBankAccount(w http.ResponseWriter, account BankAccount) {
	httpserver.WriteJSON(w, http.StatusOK, bankAccountResponse{
		IBAN:       account.IBAN,
		HolderName: account.HolderName,
		UpdatedAt:  account.UpdatedAt.UTC(),
	})
}

// requestUser returns the user set by RequireAuth. Reaching a handler without one is a
// wiring bug, answered with 500.
func (h *Handler) requestUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	userID, ok := httpserver.UserID(r.Context())
	if !ok {
		h.log.ErrorContext(r.Context(), "identity: request without authenticated user")
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
	}
	return userID, ok
}

// RequireAuth accepts only requests with a valid "Authorization: Bearer <jwt>" of an active
// user and puts the user ID in the request context (httpserver.UserID).
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			httpserver.WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "Missing or invalid access token.")
			return
		}
		userID, err := h.svc.Authenticate(r.Context(), token)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(httpserver.WithUserID(r.Context(), userID)))
	})
}

// writeError maps service errors to the API contract (T004).
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidPhone):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidPhone, "Phone must be an Egyptian mobile number in E.164, e.g. +201001234567.")
	case errors.Is(err, ErrInvalidPassword):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidPassword, "Password must be 8 to 72 bytes long.")
	case errors.Is(err, ErrInvalidName):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidName, "Full name must be 2 to 100 characters long.")
	case errors.Is(err, ErrPhoneTaken):
		httpserver.WriteError(w, http.StatusConflict, CodePhoneAlreadyRegistered, "This phone number is already registered.")
	case errors.Is(err, ErrInvalidCredentials):
		httpserver.WriteError(w, http.StatusUnauthorized, CodeInvalidCredentials, "Phone or password is incorrect.")
	case errors.Is(err, ErrUserDisabled):
		httpserver.WriteError(w, http.StatusForbidden, CodeUserDisabled, "This user is disabled.")
	case errors.Is(err, ErrInvalidIBAN):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidIBAN, "IBAN must be a valid Egyptian IBAN (EG followed by 27 digits).")
	case errors.Is(err, ErrInvalidHolderName):
		httpserver.WriteError(w, http.StatusBadRequest, CodeInvalidHolderName, "Holder name must be 2 to 100 characters long.")
	case errors.Is(err, ErrBankAccountNotFound):
		httpserver.WriteError(w, http.StatusNotFound, CodeBankAccountNotFound, "No bank account is linked yet.")
	case errors.Is(err, ErrRecipientNotFound):
		httpserver.WriteError(w, http.StatusNotFound, CodeRecipientNotFound, "No user is registered with this phone number.")
	case errors.Is(err, ErrSelfRecipient):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, CodeSelfTransferNotAllowed, "You cannot send money to yourself.")
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "Missing or invalid access token.")
	case errors.Is(err, database.ErrTryAgain):
		httpserver.WriteError(w, http.StatusServiceUnavailable, httpserver.CodeTryAgain, "The request conflicted with another one. Please try again.")
	default:
		h.log.ErrorContext(r.Context(), "identity: request failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
	}
}

// bearerToken returns the token of an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}
