package router

import (
	"context"
	"net/http"
	"time"

	"loyaltyledger/internal/domain"
	"loyaltyledger/internal/handler"
	"loyaltyledger/internal/middleware"
)

func New(h *handler.Handler, auth middleware.Authenticator, ping func(context.Context) error, debug bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/user/register", h.Register)
	mux.HandleFunc("POST /api/user/login", h.Login)
	mux.Handle("GET /api/user/me", middleware.Auth(auth, http.HandlerFunc(h.Me)))
	user := func(fn http.HandlerFunc) http.Handler {
		return middleware.Auth(auth, middleware.RequireRole(domain.RoleUser, fn))
	}
	admin := func(fn http.HandlerFunc) http.Handler {
		return middleware.Auth(auth, middleware.RequireRole(domain.RoleAdmin, fn))
	}
	mux.Handle("POST /api/user/orders", user(h.CreateOrder))
	mux.Handle("GET /api/user/orders", user(h.GetOrders))
	mux.Handle("GET /api/user/balance", user(h.GetBalance))
	mux.Handle("POST /api/user/balance/withdraw", user(h.Withdraw))
	mux.Handle("GET /api/user/withdrawals", user(h.GetWithdrawals))
	mux.Handle("GET /api/admin/users", admin(h.ListUsers))
	mux.Handle("GET /api/admin/orders", admin(h.ListOrders))
	mux.Handle("PATCH /api/admin/users/{id}/block", admin(h.SetBlocked))
	mux.Handle("GET /api/admin/stats", admin(h.GetStats))
	mux.Handle("GET /api/admin/stats/export", admin(h.ExportStats))
	// Дополнительный маршрут экспорта статистики.
	mux.Handle("POST /api/stats/export", admin(h.ExportStats))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			handler.WriteError(w, 503, "NOT_READY", "база данных недоступна")
			return
		}
		w.WriteHeader(200)
	})
	return middleware.Logging(debug, middleware.Recover(mux))
}
