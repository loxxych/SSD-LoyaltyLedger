package auth

import (
	"context"
	"errors"
	"loyaltyledger/internal/domain"
	"testing"
	"time"
)

type sessions struct {
	hash   string
	expiry time.Time
	user   *domain.User
}

func (s *sessions) CreateSession(_ context.Context, id int64, hash string, expiry time.Time) error {
	s.hash = hash
	s.expiry = expiry
	return nil
}
func (s *sessions) GetSessionUser(_ context.Context, hash string) (*domain.User, error) {
	if hash != s.hash || s.expiry.Before(time.Now()) {
		return nil, domain.ErrInvalidToken
	}
	return s.user, nil
}
func TestTokens(t *testing.T) {
	repo := &sessions{user: &domain.User{ID: 7, Role: domain.RoleUser}}
	manager := New(repo, time.Hour)
	ctx := context.Background()
	token, err := manager.GenerateToken(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || repo.hash == token {
		t.Fatal("unsafe token storage")
	}
	if user, err := manager.Authenticate(ctx, token); err != nil || user.ID != 7 {
		t.Fatalf("%v %v", user, err)
	}
	repo.user.Blocked = true
	if _, err := manager.Authenticate(ctx, token); !errors.Is(err, domain.ErrBlocked) {
		t.Fatal(err)
	}
	repo.user.Blocked = false
	repo.expiry = time.Now().Add(-time.Second)
	if _, err := manager.Authenticate(ctx, token); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(ctx, "7"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatal(err)
	}
}
