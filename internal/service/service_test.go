package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"loyaltyledger/internal/domain"
	"loyaltyledger/internal/orders"
	"loyaltyledger/internal/utils"
)

type fakeRepo struct {
	Repository
	existing  *domain.Order
	prior     *domain.Withdrawal
	created   bool
	debited   bool
	finalized string
	amount    domain.Money
	retried   bool
	user      *domain.User
}

func (f *fakeRepo) GetOrder(context.Context, string) (*domain.Order, error) {
	if f.existing == nil {
		return nil, domain.ErrOrderNotFound
	}
	return f.existing, nil
}
func (f *fakeRepo) CreateOrder(_ context.Context, id int64, number, status string, rate int64) (*domain.Order, error) {
	f.created = true
	return &domain.Order{UserID: id, Number: number, ExternalStatus: status, RewardPercent: rate}, nil
}
func (f *fakeRepo) GetWithdrawal(context.Context, string) (*domain.Withdrawal, error) {
	return f.prior, nil
}
func (f *fakeRepo) Withdraw(context.Context, int64, string, domain.Money) error {
	f.debited = true
	return nil
}
func (f *fakeRepo) FinalizeOrder(_ context.Context, _ domain.Order, status, _ string, amount domain.Money) error {
	f.finalized = status
	f.amount = amount
	return nil
}
func (f *fakeRepo) RetryOrder(context.Context, string, string, time.Duration) error {
	f.retried = true
	return nil
}
func (f *fakeRepo) GetUserByLogin(context.Context, string) (*domain.User, error) {
	if f.user == nil {
		return nil, domain.ErrUserNotFound
	}
	return f.user, nil
}
func (f *fakeRepo) CreateUser(_ context.Context, login, hash string) (*domain.User, error) {
	if f.user != nil {
		return nil, domain.ErrUserExists
	}
	f.user = &domain.User{ID: 1, Login: login, PasswordHash: hash, Role: domain.RoleUser}
	return f.user, nil
}
func (f *fakeRepo) CreateSession(context.Context, int64, string, time.Time) error { return nil }

type fakeOrders struct {
	info  *orders.Info
	err   error
	calls int
}

func (f *fakeOrders) GetOrder(context.Context, string) (*orders.Info, error) {
	f.calls++
	return f.info, f.err
}
func options() Options {
	return Options{Interval: time.Second, Concurrency: 2, RequestTimeout: time.Second, TokenTTL: time.Hour, RewardPercent: 5}
}

func TestCreateOrderTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, number, status string
		owner                int64
		remoteErr, want      error
	}{
		{"valid", "79927398713", orders.Completed, 7, nil, nil},
		{"wrong owner", "79927398713", orders.Completed, 9, nil, domain.ErrOrderOwner},
		{"cancelled", "79927398713", orders.Cancelled, 7, nil, domain.ErrOrderState},
		{"missing", "79927398713", "", 0, domain.ErrOrderNotFound, domain.ErrOrderNotFound},
		{"unavailable", "79927398713", "", 0, domain.ErrExternalUnavailable, domain.ErrExternalUnavailable},
		{"bad number", "123", orders.Completed, 7, nil, domain.ErrInvalidOrder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{}
			remote := &fakeOrders{info: &orders.Info{OwnerID: tc.owner, Status: tc.status}, err: tc.remoteErr}
			_, err := New(repo, remote, options()).CreateOrder(context.Background(), 7, tc.number)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if repo.created != (tc.want == nil) {
				t.Fatal("unexpected local insert")
			}
			if tc.want == domain.ErrInvalidOrder && remote.calls != 0 {
				t.Fatal("invalid number reached remote")
			}
		})
	}
}
func TestCreateOrderDuplicateWithoutRemote(t *testing.T) {
	for _, id := range []int64{7, 8} {
		repo := &fakeRepo{existing: &domain.Order{UserID: 7}}
		remote := &fakeOrders{err: domain.ErrExternalUnavailable}
		_, err := New(repo, remote, options()).CreateOrder(context.Background(), id, "79927398713")
		want := domain.ErrOrderOwnedByUser
		if id != 7 {
			want = domain.ErrOrderExists
		}
		if !errors.Is(err, want) || remote.calls != 0 {
			t.Fatalf("id=%d err=%v calls=%d", id, err, remote.calls)
		}
	}
}
func TestWorkerUsesRealState(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		owner        int64
		remoteErr    error
		terminal     string
		retry        bool
		amount       domain.Money
	}{
		{"completed", orders.Completed, 7, nil, domain.OrderStatusProcessed, false, 501},
		{"paid", orders.Paid, 7, nil, "", true, 0},
		{"created", orders.Created, 7, nil, "", true, 0},
		{"cancelled", orders.Cancelled, 7, nil, domain.OrderStatusInvalid, false, 0},
		{"owner changed", orders.Completed, 8, nil, domain.OrderStatusInvalid, false, 0},
		{"missing", "", 0, domain.ErrOrderNotFound, domain.OrderStatusInvalid, false, 0},
		{"timeout", "", 0, domain.ErrExternalUnavailable, "", true, 0},
		{"bad response", "", 0, domain.ErrExternalResponse, "", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{}
			remote := &fakeOrders{info: &orders.Info{OwnerID: tc.owner, Status: tc.status, Amount: 10039}, err: tc.remoteErr}
			_ = New(repo, remote, options()).processOrder(context.Background(), domain.Order{UserID: 7, Number: "79927398713", RewardPercent: 5})
			if repo.finalized != tc.terminal || repo.retried != tc.retry || repo.amount != tc.amount {
				t.Fatalf("state=%s retry=%v amount=%d", repo.finalized, repo.retried, repo.amount)
			}
		})
	}
}
func TestWithdrawalValidation(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		owner        int64
		amount       domain.Money
		want         error
	}{
		{"valid", orders.Created, 7, 1000, nil},
		{"zero", orders.Created, 7, 0, domain.ErrInvalidAmount},
		{"negative", orders.Created, 7, -1, domain.ErrInvalidAmount},
		{"over order amount", orders.Created, 7, 10001, domain.ErrInvalidAmount},
		{"foreign order", orders.Created, 8, 1000, domain.ErrOrderOwner},
		{"already paid", orders.Paid, 7, 1000, domain.ErrOrderState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{}
			remote := &fakeOrders{info: &orders.Info{OwnerID: tc.owner, Status: tc.status, Amount: 10000}}
			err := New(repo, remote, options()).Withdraw(context.Background(), 7, "12345678903", tc.amount)
			if !errors.Is(err, tc.want) || repo.debited != (tc.want == nil) {
				t.Fatalf("err=%v debit=%v", err, repo.debited)
			}
		})
	}
}
func TestCommittedWithdrawalRetry(t *testing.T) {
	repo := &fakeRepo{prior: &domain.Withdrawal{UserID: 7, Sum: 1000}}
	remote := &fakeOrders{err: domain.ErrExternalUnavailable}
	svc := New(repo, remote, options())
	if err := svc.Withdraw(context.Background(), 7, "12345678903", 1000); err != nil {
		t.Fatal(err)
	}
	if err := svc.Withdraw(context.Background(), 7, "12345678903", 1100); !errors.Is(err, domain.ErrWithdrawalConflict) {
		t.Fatal(err)
	}
	if remote.calls != 0 || repo.debited {
		t.Fatal("repeat contacted shop or debited twice")
	}
}
func TestRegistrationAndLogin(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo, &fakeOrders{}, options())
	ctx := context.Background()
	if _, err := svc.RegisterUser(ctx, "ab", "short"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatal(err)
	}
	token, err := svc.RegisterUser(ctx, "alice", "long-password")
	if err != nil || len(token) != 64 {
		t.Fatalf("register: %v", err)
	}
	if repo.user.PasswordHash == "long-password" || !utils.CheckPassword(repo.user.PasswordHash, "long-password") {
		t.Fatal("password not hashed")
	}
	if _, err = svc.RegisterUser(ctx, "alice", "long-password"); !errors.Is(err, domain.ErrUserExists) {
		t.Fatal(err)
	}
	token2, err := svc.LoginUser(ctx, "alice", "long-password")
	if err != nil || token2 == token {
		t.Fatalf("login: %v", err)
	}
	if _, err = svc.LoginUser(ctx, "alice", "wrong"); !errors.Is(err, domain.ErrInvalidPassword) {
		t.Fatal(err)
	}
	repo.user.Blocked = true
	if _, err = svc.LoginUser(ctx, "alice", "long-password"); !errors.Is(err, domain.ErrBlocked) {
		t.Fatal(err)
	}
}
