// Пакет handler содержит HTTP-обработчики.
package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"loyaltyledger/internal/domain"
)

type Service interface {
	RegisterUser(context.Context, string, string) (string, error)
	LoginUser(context.Context, string, string) (string, error)
	Authenticate(context.Context, string) (*domain.User, error)
	CreateOrder(context.Context, int64, string) (*domain.Order, error)
	GetUserOrders(context.Context, int64, int, int) ([]domain.Order, error)
	GetBalance(context.Context, int64) (domain.Balance, error)
	Withdraw(context.Context, int64, string, domain.Money) error
	GetWithdrawals(context.Context, int64, int, int) ([]domain.Withdrawal, error)
	ListUsers(context.Context, int, int) ([]domain.User, error)
	ListOrders(context.Context, int, int) ([]domain.Order, error)
	SetBlocked(context.Context, int64, bool) error
	GetStats(context.Context) (*domain.Stats, error)
}
type Handler struct{ svc Service }

func New(svc Service) *Handler { return &Handler{svc: svc} }

type contextKey int

const userKey contextKey = 0

func WithUser(ctx context.Context, user *domain.User) context.Context {
	return context.WithValue(ctx, userKey, user)
}
func UserFromContext(ctx context.Context) (*domain.User, bool) {
	u, ok := ctx.Value(userKey).(*domain.User)
	return u, ok && u != nil
}
func UserIDFromContext(ctx context.Context) (int64, bool) {
	u, ok := UserFromContext(ctx)
	if !ok {
		return 0, false
	}
	return u.ID, true
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message})
}
func respondError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	message := "внутренняя ошибка сервера"
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials):
		status, code = 400, "INVALID_CREDENTIALS"
	case errors.Is(err, domain.ErrUserExists):
		status, code = 409, "USER_EXISTS"
	case errors.Is(err, domain.ErrInvalidPassword), errors.Is(err, domain.ErrInvalidToken):
		status, code = 401, "UNAUTHORIZED"
	case errors.Is(err, domain.ErrBlocked):
		status, code = 403, "USER_BLOCKED"
	case errors.Is(err, domain.ErrForbidden):
		status, code = 403, "FORBIDDEN"
	case errors.Is(err, domain.ErrOrderOwner):
		status, code = 403, "ORDER_OWNER_MISMATCH"
	case errors.Is(err, domain.ErrOrderExists):
		status, code = 409, "ORDER_EXISTS"
	case errors.Is(err, domain.ErrOrderState):
		status, code = 409, "ORDER_STATE"
	case errors.Is(err, domain.ErrWithdrawalConflict):
		status, code = 409, "WITHDRAWAL_CONFLICT"
	case errors.Is(err, domain.ErrInvalidOrder):
		status, code = 422, "INVALID_ORDER"
	case errors.Is(err, domain.ErrInvalidAmount):
		status, code = 422, "INVALID_AMOUNT"
	case errors.Is(err, domain.ErrInsufficientFunds):
		status, code = 402, "INSUFFICIENT_FUNDS"
	case errors.Is(err, domain.ErrOrderNotFound):
		status, code = 404, "ORDER_NOT_FOUND"
	case errors.Is(err, domain.ErrUserNotFound):
		status, code = 404, "USER_NOT_FOUND"
	case errors.Is(err, domain.ErrExternalUnavailable):
		status, code = 503, "ORDER_SERVICE_UNAVAILABLE"
	case errors.Is(err, domain.ErrExternalResponse):
		status, code = 502, "ORDER_SERVICE_RESPONSE"
	}
	if status != 500 {
		message = err.Error()
	} else {
		log.Printf("request failed: %v", err)
	}
	WriteError(w, status, code, message)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
func decode(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dest)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = errors.New("extra JSON data")
		}
	}
	if err != nil {
		WriteError(w, 400, "BAD_REQUEST", "ожидается один JSON-объект с допустимыми полями")
		return false
	}
	return true
}
func credentials(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return "", "", false
	}
	return req.Login, req.Password, true
}
func tokenResponse(w http.ResponseWriter, token string) {
	w.Header().Set("Authorization", token)
	writeJSON(w, 200, map[string]string{"token": token, "token_type": "Bearer"})
}
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	login, password, ok := credentials(w, r)
	if !ok {
		return
	}
	token, err := h.svc.RegisterUser(r.Context(), login, password)
	if err != nil {
		respondError(w, err)
		return
	}
	tokenResponse(w, token)
}
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	login, password, ok := credentials(w, r)
	if !ok {
		return
	}
	token, err := h.svc.LoginUser(r.Context(), login, password)
	if err != nil {
		respondError(w, err)
		return
	}
	tokenResponse(w, token)
}
func requireID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := UserIDFromContext(r.Context())
	if !ok {
		respondError(w, domain.ErrInvalidToken)
	}
	return id, ok
}
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		respondError(w, domain.ErrInvalidToken)
		return
	}
	writeJSON(w, 200, u)
}
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128))
	if err != nil {
		WriteError(w, 400, "BAD_REQUEST", "ожидается номер заказа")
		return
	}
	_, err = h.svc.CreateOrder(r.Context(), id, strings.TrimSpace(string(body)))
	if errors.Is(err, domain.ErrOrderOwnedByUser) {
		w.WriteHeader(200)
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(202)
}
func pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 50, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if value := r.URL.Query().Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				WriteError(w, 400, "BAD_REQUEST", "неверная пагинация")
				return 0, 0, false
			}
			*target = n
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		WriteError(w, 400, "BAD_REQUEST", "limit: 1–100, offset: от 0")
		return 0, 0, false
	}
	return limit, offset, true
}
func (h *Handler) GetOrders(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	data, err := h.svc.GetUserOrders(r.Context(), id, limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}
	if len(data) == 0 {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	data, err := h.svc.GetBalance(r.Context(), id)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	var req struct {
		Order string       `json:"order"`
		Sum   domain.Money `json:"sum"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := h.svc.Withdraw(r.Context(), id, req.Order, req.Sum); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(200)
}
func (h *Handler) GetWithdrawals(w http.ResponseWriter, r *http.Request) {
	id, ok := requireID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	data, err := h.svc.GetWithdrawals(r.Context(), id, limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}
	if len(data) == 0 {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	data, err := h.svc.ListUsers(r.Context(), limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pagination(w, r)
	if !ok {
		return
	}
	data, err := h.svc.ListOrders(r.Context(), limit, offset)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) SetBlocked(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, 400, "BAD_REQUEST", "неверный ID пользователя")
		return
	}
	var req struct {
		Blocked *bool `json:"blocked"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Blocked == nil {
		WriteError(w, 400, "BAD_REQUEST", "поле blocked обязательно")
		return
	}
	if err = h.svc.SetBlocked(r.Context(), id, *req.Blocked); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	data, err := h.svc.GetStats(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, data)
}
func (h *Handler) ExportStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.GetStats(r.Context())
	if err != nil {
		respondError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="loyaltyledger-stats.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	output := csv.NewWriter(w)
	records := [][]string{{"metric", "value"}, {"users", strconv.FormatInt(stats.UserCount, 10)}, {"blocked_users", strconv.FormatInt(stats.BlockedUsers, 10)},
		{"orders", strconv.FormatInt(stats.OrdersCount, 10)}, {"total_accrued", stats.TotalAccrued.String()}, {"total_withdrawn", stats.TotalWithdrawn.String()},
		{"generated_at", stats.GeneratedAt.UTC().Format(time.RFC3339)}}
	for _, status := range []string{"NEW", "PROCESSING", "PROCESSED", "INVALID"} {
		records = append(records, []string{"orders_" + status, strconv.FormatInt(stats.OrdersByStatus[status], 10)})
	}
	if err = output.WriteAll(records); err != nil {
		log.Printf("export: %v", err)
	}
}
