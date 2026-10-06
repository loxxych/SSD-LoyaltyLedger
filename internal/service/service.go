// Пакет service содержит бизнес-логику и фоновую обработку заказов.
package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"loyaltyledger/internal/auth"
	"loyaltyledger/internal/domain"
	"loyaltyledger/internal/orders"
	"loyaltyledger/internal/utils"
)

type Repository interface {
	auth.SessionRepository
	CreateUser(context.Context, string, string) (*domain.User, error)
	GetUserByLogin(context.Context, string) (*domain.User, error)
	EnsureAdmin(context.Context, string, string) error
	ListUsers(context.Context, int, int) ([]domain.User, error)
	SetBlocked(context.Context, int64, bool) error
	GetOrder(context.Context, string) (*domain.Order, error)
	CreateOrder(context.Context, int64, string, string, int64) (*domain.Order, error)
	ListOrders(context.Context, int64, int, int) ([]domain.Order, error)
	ClaimOrders(context.Context, int, time.Duration) ([]domain.Order, error)
	RetryOrder(context.Context, string, string, time.Duration) error
	FinalizeOrder(context.Context, domain.Order, string, string, domain.Money) error
	GetBalance(context.Context, int64) (domain.Balance, error)
	GetWithdrawal(context.Context, string) (*domain.Withdrawal, error)
	Withdraw(context.Context, int64, string, domain.Money) error
	GetWithdrawals(context.Context, int64, int, int) ([]domain.Withdrawal, error)
	GetStats(context.Context) (*domain.Stats, error)
}
type OrderService interface {
	GetOrder(context.Context, string) (*orders.Info, error)
}
type Options struct {
	Interval       time.Duration
	Concurrency    int
	RequestTimeout time.Duration
	TokenTTL       time.Duration
	RewardPercent  int64
}
type Service struct {
	repo    Repository
	orders  OrderService
	auth    *auth.Manager
	options Options
}

func New(repo Repository, external OrderService, options Options) *Service {
	return &Service{repo: repo, orders: external, auth: auth.New(repo, options.TokenTTL), options: options}
}
func validCredentials(login, password string) bool {
	if !utf8.ValidString(login) || utf8.RuneCountInString(login) < 3 || utf8.RuneCountInString(login) > 64 || len(password) < 8 || len(password) > 72 {
		return false
	}
	for _, c := range login {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func (s *Service) RegisterUser(ctx context.Context, login, password string) (string, error) {
	if !validCredentials(login, password) {
		return "", domain.ErrInvalidCredentials
	}
	hash, err := utils.HashPassword(password)
	if err != nil {
		return "", err
	}
	user, err := s.repo.CreateUser(ctx, login, hash)
	if err != nil {
		return "", err
	}
	return s.auth.GenerateToken(ctx, user.ID)
}
func (s *Service) LoginUser(ctx context.Context, login, password string) (string, error) {
	if login == "" || len(login) > 256 || len(password) > 72 {
		return "", domain.ErrInvalidPassword
	}
	user, err := s.repo.GetUserByLogin(ctx, login)
	if errors.Is(err, domain.ErrUserNotFound) {
		return "", domain.ErrInvalidPassword
	}
	if err != nil {
		return "", err
	}
	if !utils.CheckPassword(user.PasswordHash, password) {
		return "", domain.ErrInvalidPassword
	}
	if user.Blocked {
		return "", domain.ErrBlocked
	}
	return s.auth.GenerateToken(ctx, user.ID)
}
func (s *Service) EnsureAdmin(ctx context.Context, login, password string) error {
	if login == "" && password == "" {
		return nil
	}
	if !validCredentials(login, password) {
		return domain.ErrInvalidCredentials
	}
	hash, err := utils.HashPassword(password)
	if err != nil {
		return err
	}
	return s.repo.EnsureAdmin(ctx, login, hash)
}
func (s *Service) Authenticate(ctx context.Context, token string) (*domain.User, error) {
	return s.auth.Authenticate(ctx, token)
}
func (s *Service) CreateOrder(ctx context.Context, userID int64, number string) (*domain.Order, error) {
	number = strings.TrimSpace(number)
	if !utils.ValidateLuhn(number) {
		return nil, domain.ErrInvalidOrder
	}
	old, err := s.repo.GetOrder(ctx, number)
	if err == nil {
		if old.UserID == userID {
			return nil, domain.ErrOrderOwnedByUser
		}
		return nil, domain.ErrOrderExists
	}
	if !errors.Is(err, domain.ErrOrderNotFound) {
		return nil, err
	}
	info, err := s.orders.GetOrder(ctx, number)
	if err != nil {
		return nil, err
	}
	if info.OwnerID != userID {
		return nil, domain.ErrOrderOwner
	}
	if info.Status == orders.Cancelled {
		return nil, domain.ErrOrderState
	}
	return s.repo.CreateOrder(ctx, userID, number, info.Status, s.options.RewardPercent)
}
func (s *Service) GetUserOrders(ctx context.Context, userID int64, limit, offset int) ([]domain.Order, error) {
	return s.repo.ListOrders(ctx, userID, limit, offset)
}
func (s *Service) GetBalance(ctx context.Context, userID int64) (domain.Balance, error) {
	return s.repo.GetBalance(ctx, userID)
}
func (s *Service) Withdraw(ctx context.Context, userID int64, number string, sum domain.Money) error {
	if !utils.ValidateLuhn(number) {
		return domain.ErrInvalidOrder
	}
	if sum <= 0 || sum > domain.MaxAmount {
		return domain.ErrInvalidAmount
	}
	prior, err := s.repo.GetWithdrawal(ctx, number)
	if err != nil {
		return err
	}
	// Повтор выполненного списания не зависит от текущего состояния заказа.
	if prior != nil {
		if prior.UserID == userID && prior.Sum == sum {
			return nil
		}
		return domain.ErrWithdrawalConflict
	}
	info, err := s.orders.GetOrder(ctx, number)
	if err != nil {
		return err
	}
	if info.OwnerID != userID {
		return domain.ErrOrderOwner
	}
	if info.Status != orders.Created {
		return domain.ErrOrderState
	}
	if sum > info.Amount {
		return domain.ErrInvalidAmount
	}
	return s.repo.Withdraw(ctx, userID, number, sum)
}
func (s *Service) GetWithdrawals(ctx context.Context, userID int64, limit, offset int) ([]domain.Withdrawal, error) {
	return s.repo.GetWithdrawals(ctx, userID, limit, offset)
}
func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	return s.repo.ListUsers(ctx, limit, offset)
}
func (s *Service) ListOrders(ctx context.Context, limit, offset int) ([]domain.Order, error) {
	return s.repo.ListOrders(ctx, 0, limit, offset)
}
func (s *Service) SetBlocked(ctx context.Context, id int64, blocked bool) error {
	return s.repo.SetBlocked(ctx, id, blocked)
}
func (s *Service) GetStats(ctx context.Context) (*domain.Stats, error) { return s.repo.GetStats(ctx) }

func (s *Service) StartAccrualWorker(ctx context.Context) {
	ticker := time.NewTicker(s.options.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s.processAllPendingOrders(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) processAllPendingOrders(ctx context.Context) {
	// Число захваченных заказов ограничено числом свободных обработчиков.
	queryCtx, cancel := context.WithTimeout(ctx, s.options.RequestTimeout)
	pending, err := s.repo.ClaimOrders(queryCtx, s.options.Concurrency, 2*s.options.RequestTimeout+time.Minute)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("claim orders: %v", err)
		}
		return
	}
	var wg sync.WaitGroup
	for _, order := range pending {
		wg.Add(1)
		go func(order domain.Order) {
			defer wg.Done()
			taskCtx, cancel := context.WithTimeout(ctx, s.options.RequestTimeout)
			defer cancel()
			if err := s.processOrder(taskCtx, order); err != nil && ctx.Err() == nil {
				log.Printf("process order %s: %v", order.Number, err)
			}
		}(order)
	}
	wg.Wait()
}
func (s *Service) processOrder(ctx context.Context, order domain.Order) error {
	info, err := s.orders.GetOrder(ctx, order.Number)
	if errors.Is(err, domain.ErrOrderNotFound) {
		return s.repo.FinalizeOrder(ctx, order, domain.OrderStatusInvalid, "NOT_FOUND", 0)
	}
	if err != nil {
		// Ошибка внешнего сервиса оставляет заказ доступным для повторной обработки.
		if retryErr := s.repo.RetryOrder(ctx, order.Number, "", s.options.Interval); retryErr != nil {
			return retryErr
		}
		return err
	}
	if info.OwnerID != order.UserID || info.Status == orders.Cancelled {
		return s.repo.FinalizeOrder(ctx, order, domain.OrderStatusInvalid, info.Status, 0)
	}
	if info.Status != orders.Completed {
		return s.repo.RetryOrder(ctx, order.Number, info.Status, s.options.Interval)
	}
	// Ставка фиксируется при загрузке. Результат округляется вниз до сотой доли балла.
	accrual := domain.Money(int64(info.Amount) * order.RewardPercent / 100)
	return s.repo.FinalizeOrder(ctx, order, domain.OrderStatusProcessed, info.Status, accrual)
}
