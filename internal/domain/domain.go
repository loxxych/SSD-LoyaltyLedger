// Пакет domain содержит модели и ошибки программы лояльности.
package domain

import (
	"errors"
	"time"
)

var (
	ErrUserExists          = errors.New("логин уже занят")
	ErrUserNotFound        = errors.New("пользователь не найден")
	ErrInvalidPassword     = errors.New("неверный логин или пароль")
	ErrInvalidCredentials  = errors.New("логин: 3–64 символа; пароль: 8–72 байта")
	ErrInvalidToken        = errors.New("токен недействителен или истёк")
	ErrBlocked             = errors.New("пользователь заблокирован")
	ErrForbidden           = errors.New("недостаточно прав")
	ErrOrderExists         = errors.New("заказ уже загружен другим пользователем")
	ErrOrderOwnedByUser    = errors.New("заказ уже загружен этим пользователем")
	ErrOrderNotFound       = errors.New("заказ не найден в Order Service")
	ErrOrderOwner          = errors.New("заказ принадлежит другому пользователю")
	ErrOrderState          = errors.New("состояние заказа не допускает эту операцию")
	ErrInsufficientFunds   = errors.New("недостаточно баллов")
	ErrInvalidOrder        = errors.New("неверный номер заказа")
	ErrInvalidAmount       = errors.New("сумма должна быть положительной, не более двух знаков после точки")
	ErrWithdrawalConflict  = errors.New("для заказа уже выполнено списание другой суммы")
	ErrExternalUnavailable = errors.New("Order Service временно недоступен")
	ErrExternalResponse    = errors.New("некорректный ответ Order Service")
)

const (
	RoleUser              = "user"
	RoleAdmin             = "admin"
	OrderStatusNew        = "NEW"
	OrderStatusProcessing = "PROCESSING"
	OrderStatusInvalid    = "INVALID"
	OrderStatusProcessed  = "PROCESSED"
)

type User struct {
	ID           int64     `json:"id"`
	Login        string    `json:"login"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Blocked      bool      `json:"blocked"`
	CreatedAt    time.Time `json:"created_at"`
}

// Status — состояние обработки бонусов, ExternalStatus — последнее состояние заказа в магазине.
type Order struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Number         string    `json:"number"`
	Status         string    `json:"status"`
	ExternalStatus string    `json:"external_status"`
	Accrual        Money     `json:"accrual"`
	RewardPercent  int64     `json:"-"`
	UploadedAt     time.Time `json:"uploaded_at"`
}

type Balance struct {
	Current   Money `json:"current"`
	Withdrawn Money `json:"withdrawn"`
}

type Withdrawal struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	OrderNumber string    `json:"order"`
	Sum         Money     `json:"sum"`
	ProcessedAt time.Time `json:"processed_at"`
}

type Stats struct {
	UserCount      int64            `json:"user_count"`
	BlockedUsers   int64            `json:"blocked_users"`
	OrdersCount    int64            `json:"orders_count"`
	OrdersByStatus map[string]int64 `json:"orders_by_status"`
	TotalAccrued   Money            `json:"total_accrued"`
	TotalWithdrawn Money            `json:"total_withdrawn"`
	GeneratedAt    time.Time        `json:"generated_at"`
}
