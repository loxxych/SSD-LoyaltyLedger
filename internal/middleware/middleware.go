package middleware

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"loyaltyledger/internal/domain"
	"loyaltyledger/internal/handler"
)

type Authenticator interface {
	Authenticate(context.Context, string) (*domain.User, error)
}

func Auth(auth Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		token := ""
		if len(parts) == 1 {
			token = parts[0]
		} else if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token = parts[1]
		}
		if token == "" {
			handler.WriteError(w, 401, "UNAUTHORIZED", "требуется токен")
			return
		}
		user, err := auth.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, domain.ErrBlocked) {
				handler.WriteError(w, 403, "USER_BLOCKED", err.Error())
			} else if errors.Is(err, domain.ErrInvalidToken) {
				handler.WriteError(w, 401, "UNAUTHORIZED", "токен недействителен или истёк")
			} else {
				log.Printf("authentication failed: %v", err)
				handler.WriteError(w, 500, "INTERNAL_ERROR", "ошибка проверки доступа")
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(handler.WithUser(r.Context(), user)))
	})
}
func RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := handler.UserFromContext(r.Context())
		if !ok {
			handler.WriteError(w, 401, "UNAUTHORIZED", "требуется токен")
			return
		}
		if user.Role != role {
			handler.WriteError(w, 403, "FORBIDDEN", "недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func Logging(debug bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = 200
		}
		log.Printf("%s %s status=%d duration=%s", r.Method, r.URL.Path, status, time.Since(start))
		if debug {
			log.Printf("request bytes=%d protocol=%s", r.ContentLength, r.Proto)
		}
	})
}
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				handler.WriteError(w, 500, "INTERNAL_ERROR", "внутренняя ошибка сервера")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
