// Пакет auth создаёт токены и проверяет сессии. В БД хранятся SHA-256-хеши токенов.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"loyaltyledger/internal/domain"
	"time"
)

type SessionRepository interface {
	CreateSession(context.Context, int64, string, time.Time) error
	GetSessionUser(context.Context, string) (*domain.User, error)
}
type Manager struct {
	repo SessionRepository
	ttl  time.Duration
}

func New(repo SessionRepository, ttl time.Duration) *Manager { return &Manager{repo: repo, ttl: ttl} }
func digest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (m *Manager) GenerateToken(ctx context.Context, userID int64) (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(data[:])
	if err := m.repo.CreateSession(ctx, userID, digest(token), time.Now().Add(m.ttl)); err != nil {
		return "", err
	}
	return token, nil
}
func (m *Manager) Authenticate(ctx context.Context, token string) (*domain.User, error) {
	if len(token) != 64 {
		return nil, domain.ErrInvalidToken
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil, domain.ErrInvalidToken
	}
	user, err := m.repo.GetSessionUser(ctx, digest(token))
	if err != nil {
		return nil, err
	}
	if user.Blocked {
		return nil, domain.ErrBlocked
	}
	return user, nil
}
