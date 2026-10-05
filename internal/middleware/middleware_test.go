package middleware

import (
	"context"
	"loyaltyledger/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

type authFake struct {
	role string
	err  error
}

func (a authFake) Authenticate(context.Context, string) (*domain.User, error) {
	return &domain.User{ID: 7, Role: a.role}, a.err
}
func TestRoleBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, header, role string
		err                error
		status             int
	}{
		{"anonymous", "", domain.RoleUser, nil, 401},
		{"user denied", "Bearer token", domain.RoleUser, nil, 403},
		{"admin allowed", "Bearer token", domain.RoleAdmin, nil, 204},
		{"raw token compatibility", "token", domain.RoleAdmin, nil, 204},
		{"blocked", "Bearer token", domain.RoleAdmin, domain.ErrBlocked, 403},
		{"invalid", "Bearer token", domain.RoleAdmin, domain.ErrInvalidToken, 401},
		{"malformed header", "Basic abc", domain.RoleAdmin, nil, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
			route := Auth(authFake{tc.role, tc.err}, RequireRole(domain.RoleAdmin, target))
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()
			route.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d want %d", w.Code, tc.status)
			}
		})
	}
}
