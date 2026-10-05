package handler

import (
	"context"
	"errors"
	"loyaltyledger/internal/domain"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeService struct {
	Service
	err    error
	called bool
}

func (f *fakeService) RegisterUser(context.Context, string, string) (string, error) {
	f.called = true
	return "token", f.err
}
func (f *fakeService) CreateOrder(context.Context, int64, string) (*domain.Order, error) {
	f.called = true
	return nil, f.err
}
func (f *fakeService) GetUserOrders(context.Context, int64, int, int) ([]domain.Order, error) {
	return nil, f.err
}
func (f *fakeService) GetStats(context.Context) (*domain.Stats, error) {
	return &domain.Stats{UserCount: 2, OrdersByStatus: map[string]int64{}}, f.err
}
func TestRegisterStrictInput(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"login":"alice","password":"long-password"}`, 200},
		{`{"login":"alice","password":"long-password","role":"admin"}`, 400},
		{`{"login":"alice"} {}`, 400},
		{`not-json`, 400},
	} {
		f := &fakeService{}
		w := httptest.NewRecorder()
		New(f).Register(w, httptest.NewRequest("POST", "/", strings.NewReader(tc.body)))
		if w.Code != tc.status || f.called != (tc.status == 200) {
			t.Fatalf("%s: %d", tc.body, w.Code)
		}
	}
}
func TestOrderErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{nil, 202}, {domain.ErrOrderOwnedByUser, 200}, {domain.ErrOrderOwner, 403}, {domain.ErrOrderExists, 409},
		{domain.ErrInvalidOrder, 422}, {domain.ErrOrderNotFound, 404}, {domain.ErrExternalUnavailable, 503}, {domain.ErrExternalResponse, 502},
		{errors.New("private DB error"), 500},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader("79927398713"))
		r = r.WithContext(WithUser(r.Context(), &domain.User{ID: 7}))
		New(&fakeService{err: tc.err}).CreateOrder(w, r)
		if w.Code != tc.code {
			t.Fatalf("%v: got %d want %d", tc.err, w.Code, tc.code)
		}
		if strings.Contains(w.Body.String(), "private DB") {
			t.Fatal("internal details leaked")
		}
	}
}
func TestEmptyOrdersHaveNoBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(WithUser(r.Context(), &domain.User{ID: 7}))
	New(&fakeService{}).GetOrders(w, r)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestExportDownloadsCSV(t *testing.T) {
	w := httptest.NewRecorder()
	New(&fakeService{}).ExportStats(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Body.String(), "users,2") {
		t.Fatal(w)
	}
}
