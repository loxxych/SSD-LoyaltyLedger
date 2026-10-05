package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"loyaltyledger/internal/domain"
	"loyaltyledger/internal/handler"
	"loyaltyledger/internal/orders"
	"loyaltyledger/internal/router"
	"loyaltyledger/internal/service"
)

// Интеграционные тесты используют отдельную схему.
func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is unset; PostgreSQL integration test skipped")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	repo, err := Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close(); _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); db.Close() })
	if err = repo.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migration not repeatable: %v", err)
	}
	return repo, parsed.String()
}
func fundUser(t *testing.T, repo *Store) (*domain.User, *domain.Order) {
	t.Helper()
	ctx := context.Background()
	u, err := repo.CreateUser(ctx, "alice", "unused-test-hash")
	if err != nil {
		t.Fatal(err)
	}
	o, err := repo.CreateOrder(ctx, u.ID, "79927398713", orders.Completed, 5)
	if err != nil {
		t.Fatal(err)
	}
	return u, o
}
func TestPostgresAccrualOnceAndPersistence(t *testing.T) {
	repo, dsn := testStore(t)
	u, o := fundUser(t, repo)
	ctx := context.Background()
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- repo.FinalizeOrder(ctx, *o, domain.OrderStatusProcessed, orders.Completed, 5000)
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := repo.GetBalance(ctx, u.ID)
	if err != nil || b.Current != 5000 {
		t.Fatalf("balance=%+v err=%v", b, err)
	}
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	b, err = second.GetBalance(ctx, u.ID)
	if err != nil || b.Current != 5000 {
		t.Fatalf("persistent balance=%+v err=%v", b, err)
	}
	if err = repo.CreateSession(ctx, u.ID, strings.Repeat("a", 64), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetSessionUser(ctx, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateSession(ctx, u.ID, strings.Repeat("b", 64), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetSessionUser(ctx, strings.Repeat("b", 64)); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatal(err)
	}
}
func TestPostgresConcurrentDebits(t *testing.T) {
	repo, _ := testStore(t)
	u, o := fundUser(t, repo)
	ctx := context.Background()
	if err := repo.FinalizeOrder(ctx, *o, domain.OrderStatusProcessed, orders.Completed, 5000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- repo.Withdraw(ctx, u.ID, fmt.Sprintf("test-order-%d", i), 800)
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, domain.ErrInsufficientFunds) {
			t.Fatal(err)
		}
	}
	b, err := repo.GetBalance(ctx, u.ID)
	if err != nil || success != 6 || b.Current != 200 || b.Withdrawn != 4800 {
		t.Fatalf("success=%d balance=%+v err=%v", success, b, err)
	}
	withdrawals, err := repo.GetWithdrawals(ctx, u.ID, 100, 0)
	if err != nil || len(withdrawals) != 6 {
		t.Fatalf("withdrawals=%d err=%v", len(withdrawals), err)
	}
}
func TestPostgresDebitIdempotenceAndRollback(t *testing.T) {
	repo, _ := testStore(t)
	u, o := fundUser(t, repo)
	ctx := context.Background()
	if err := repo.FinalizeOrder(ctx, *o, domain.OrderStatusProcessed, orders.Completed, 5000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- repo.Withdraw(ctx, u.ID, "12345678903", 1000) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Withdraw(ctx, u.ID, "12345678903", 1200); !errors.Is(err, domain.ErrWithdrawalConflict) {
		t.Fatal(err)
	}
	// Ошибка INSERT должна откатить предшествующий UPDATE баланса.
	if err := repo.Withdraw(ctx, u.ID, strings.Repeat("9", 33), 1000); err == nil {
		t.Fatal("expected varchar constraint failure")
	}
	b, err := repo.GetBalance(ctx, u.ID)
	if err != nil || b.Current != 4000 || b.Withdrawn != 1000 {
		t.Fatalf("%+v %v", b, err)
	}
}
func TestPostgresClaimsAndRecovery(t *testing.T) {
	repo, _ := testStore(t)
	u, o := fundUser(t, repo)
	ctx := context.Background()
	batch, err := repo.ClaimOrders(ctx, 5, time.Minute)
	if err != nil || len(batch) != 1 {
		t.Fatalf("%v %v", batch, err)
	}
	batch, err = repo.ClaimOrders(ctx, 5, time.Minute)
	if err != nil || len(batch) != 0 {
		t.Fatal("active lease was reclaimed")
	}
	if _, err = repo.db.Exec(`UPDATE orders SET next_attempt_at=now()-interval '1 second' WHERE number=$1`, o.Number); err != nil {
		t.Fatal(err)
	}
	batch, err = repo.ClaimOrders(ctx, 5, time.Minute)
	if err != nil || len(batch) != 1 || batch[0].Status != domain.OrderStatusProcessing {
		t.Fatal("abandoned work not recovered")
	}
	if err = repo.SetBlocked(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinalizeOrder(ctx, *o, domain.OrderStatusProcessed, orders.Completed, 5000); !errors.Is(err, domain.ErrBlocked) {
		t.Fatalf("blocked account accrued: %v", err)
	}
	if _, err = repo.db.Exec(`UPDATE orders SET next_attempt_at=now()-interval '1 second' WHERE number=$1`, o.Number); err != nil {
		t.Fatal(err)
	}
	batch, err = repo.ClaimOrders(ctx, 5, time.Minute)
	if err != nil || len(batch) != 0 {
		t.Fatal("blocked user's order was claimed")
	}
	if err = repo.SetBlocked(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	batch, err = repo.ClaimOrders(ctx, 5, time.Minute)
	if err != nil || len(batch) != 1 {
		t.Fatal("unblocked user's order did not resume")
	}
}

func TestHTTPScenariosWithPostgres(t *testing.T) {
	repo, dsn := testStore(t)
	ctx := context.Background()
	var stateMu sync.Mutex
	state := orders.Paid
	outage := false
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateMu.Lock()
		defer stateMu.Unlock()
		if outage {
			w.WriteHeader(503)
			return
		}
		number := strings.TrimPrefix(r.URL.Path, "/api/orders/")
		status := state
		owner := int64(2)
		switch number {
		case "79927398713":
		case "12345678903":
			status = orders.Created
		case "5555555555554444":
			owner = 3
			status = orders.Completed
		default:
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(orders.Info{Number: number, OwnerID: owner, Status: status, Amount: 100000})
	}))
	defer external.Close()
	opts := service.Options{Interval: 20 * time.Millisecond, Concurrency: 2, RequestTimeout: time.Second, TokenTTL: time.Hour, RewardPercent: 5}
	svc := service.New(repo, orders.New(external.URL, "", time.Second), opts)
	if err := svc.EnsureAdmin(ctx, "admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	api := router.New(handler.New(svc), svc, repo.Ping, false)
	request := func(method, path, body, token string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d body=%s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	request("GET", "/api/user/balance", "", "", 401)
	request("POST", "/api/user/register", `{"login":"alice","password":"alice-password","role":"admin"}`, "", 400)
	w := request("POST", "/api/user/register", `{"login":"alice","password":"alice-password"}`, "", 200)
	token := w.Header().Get("Authorization")
	w = request("POST", "/api/user/login", `{"login":"alice","password":"alice-password"}`, "", 200)
	token = w.Header().Get("Authorization")
	w = request("POST", "/api/user/login", `{"login":"admin","password":"admin-password"}`, "", 200)
	admin := w.Header().Get("Authorization")
	request("GET", "/api/admin/users", "", token, 403)
	request("POST", "/api/stats/export", "", token, 403)
	request("POST", "/api/user/balance/withdraw", `{"order":"12345678903","sum":10}`, admin, 403)
	w = request("GET", "/api/admin/users", "", admin, 200)
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "$2a$") {
		t.Fatal("password leaked")
	}
	bob := request("POST", "/api/user/register", `{"login":"bob","password":"bob-password"}`, "", 200).Header().Get("Authorization")
	request("POST", "/api/user/orders", "5555555555554444", token, 403)
	stateMu.Lock()
	outage = true
	stateMu.Unlock()
	request("POST", "/api/user/orders", "79927398713", token, 503)
	request("GET", "/api/user/orders", "", token, 204)
	stateMu.Lock()
	outage = false
	stateMu.Unlock()
	request("POST", "/api/user/orders", "79927398713", token, 202)
	request("POST", "/api/user/orders", "79927398713", token, 200)
	request("GET", "/api/user/orders?user_id=2", "", bob, 204)
	request("POST", "/api/user/orders", "79927398713", bob, 409)
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); svc.StartAccrualWorker(workerCtx) }()
	defer func() { cancel(); <-done }()
	waitFor := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("background condition did not complete")
	}
	waitFor(func() bool {
		o, err := repo.GetOrder(ctx, "79927398713")
		return err == nil && o.Status == domain.OrderStatusProcessing
	})
	b, err := repo.GetBalance(ctx, 2)
	if err != nil || b.Current != 0 {
		t.Fatal("PAID order accrued early")
	}
	stateMu.Lock()
	state = orders.Completed
	stateMu.Unlock()
	waitFor(func() bool { b, err := repo.GetBalance(ctx, 2); return err == nil && b.Current == 5000 })
	request("POST", "/api/user/balance/withdraw", `{"order":"12345678903","sum":20}`, token, 200)
	request("POST", "/api/user/balance/withdraw", `{"order":"12345678903","sum":20}`, token, 200)
	request("POST", "/api/user/balance/withdraw", `{"order":"12345678903","sum":21}`, token, 409)
	w = request("GET", "/api/user/balance", "", token, 200)
	var balance domain.Balance
	if err = json.Unmarshal(w.Body.Bytes(), &balance); err != nil || balance.Current != 3000 || balance.Withdrawn != 2000 {
		t.Fatalf("balance=%+v err=%v", balance, err)
	}
	request("PATCH", "/api/admin/users/2/block", `{"blocked":true}`, admin, 204)
	request("GET", "/api/user/balance", "", token, 401)
	request("POST", "/api/user/login", `{"login":"alice","password":"alice-password"}`, "", 403)
	request("PATCH", "/api/admin/users/2/block", `{"blocked":false}`, admin, 204)
	request("GET", "/api/user/balance", "", token, 401)
	w = request("POST", "/api/user/login", `{"login":"alice","password":"alice-password"}`, "", 200)
	token = w.Header().Get("Authorization")
	request("GET", "/api/user/withdrawals", "", token, 200)
	request("GET", "/api/admin/orders", "", admin, 200)
	request("GET", "/api/admin/stats/export", "", admin, 200)
	// Новый экземпляр сервиса должен прочитать сохранённую сессию.
	reopened, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replacement := service.New(reopened, orders.New(external.URL, "", time.Second), opts)
	if u, err := replacement.Authenticate(ctx, token); err != nil || u.ID != 2 {
		t.Fatalf("persisted session: %v", err)
	}
}
