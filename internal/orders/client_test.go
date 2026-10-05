package orders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"loyaltyledger/internal/domain"
)

func TestClientContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"success", 200, `{"number":"79927398713","owner_id":7,"status":"COMPLETED","amount":1000.25}`, nil},
		{"missing", 404, ``, domain.ErrOrderNotFound},
		{"rate limit", 429, ``, domain.ErrExternalUnavailable},
		{"outage", 503, ``, domain.ErrExternalUnavailable},
		{"auth error", 401, ``, domain.ErrExternalResponse},
		{"wrong number", 200, `{"number":"other","owner_id":7,"status":"PAID","amount":10}`, domain.ErrExternalResponse},
		{"missing amount", 200, `{"number":"79927398713","owner_id":7,"status":"PAID"}`, domain.ErrExternalResponse},
		{"bad state", 200, `{"number":"79927398713","owner_id":7,"status":"WHATEVER","amount":10}`, domain.ErrExternalResponse},
		{"negative amount", 200, `{"number":"79927398713","owner_id":7,"status":"PAID","amount":-10}`, domain.ErrExternalResponse},
		{"bad JSON", 200, `{`, domain.ErrExternalResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Path != "/api/orders/79927398713" {
					t.Error("bad external request")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			info, err := New(server.URL, "test-key", time.Second).GetOrder(context.Background(), "79927398713")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil && info.Amount != 100025 {
				t.Fatal(info)
			}
		})
	}
}
func TestClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	_, err := New(server.URL, "", 20*time.Millisecond).GetOrder(context.Background(), "79927398713")
	if !errors.Is(err, domain.ErrExternalUnavailable) {
		t.Fatal(err)
	}
}
