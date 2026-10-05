// Пакет store реализует хранение данных в PostgreSQL.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"loyaltyledger/internal/domain"
)

//go:embed migrations/001_init.sql
var initialSchema string

type Store struct{ db *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(73128401)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY)`); err != nil {
		return err
	}
	var applied bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=1)`).Scan(&applied); err != nil {
		return err
	}
	if !applied {
		if _, err = tx.ExecContext(ctx, initialSchema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(1)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type scanner interface{ Scan(...any) error }

const userColumns = `id,login,password_hash,role,blocked,created_at`

func scanUser(row scanner) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Login, &u.PasswordHash, &u.Role, &u.Blocked, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	return &u, err
}
func uniqueViolation(err error) bool {
	var pg *pq.Error
	return errors.As(err, &pg) && pg.Code == "23505"
}
func createUser(ctx context.Context, tx *sql.Tx, login, hash, role string) (*domain.User, error) {
	u, err := scanUser(tx.QueryRowContext(ctx, `INSERT INTO users(login,password_hash,role) VALUES($1,$2,$3) RETURNING `+userColumns, login, hash, role))
	if uniqueViolation(err) {
		return nil, domain.ErrUserExists
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO balances(user_id) VALUES($1)`, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}
func (s *Store) CreateUser(ctx context.Context, login, hash string) (*domain.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	u, err := createUser(ctx, tx, login, hash, domain.RoleUser)
	if err != nil {
		return nil, err
	}
	return u, tx.Commit()
}

// EnsureAdmin создаёт администратора, не изменяя существующий аккаунт.
func (s *Store) EnsureAdmin(ctx context.Context, login, hash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(73128402)`); err != nil {
		return err
	}
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE login=$1`, login))
	if errors.Is(err, domain.ErrUserNotFound) {
		_, err = createUser(ctx, tx, login, hash, domain.RoleAdmin)
	} else if err == nil && u.Role != domain.RoleAdmin {
		return fmt.Errorf("admin login belongs to a regular user")
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetUserByLogin(ctx context.Context, login string) (*domain.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE login=$1`, login))
}
func (s *Store) CreateSession(ctx context.Context, userID int64, hash string, expiry time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1 FOR UPDATE`, userID))
	if err != nil {
		return err
	}
	if u.Blocked {
		return domain.ErrBlocked
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1 AND expires_at<=now()`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, hash, userID, expiry); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetSessionUser(ctx context.Context, hash string) (*domain.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT u.id,u.login,u.password_hash,u.role,u.blocked,u.created_at
 FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now()`, hash))
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil, domain.ErrInvalidToken
	}
	return u, err
}
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *u)
	}
	return result, rows.Err()
}
func (s *Store) SetBlocked(ctx context.Context, id int64, blocked bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if u.Role != domain.RoleUser {
		return domain.ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET blocked=$2 WHERE id=$1`, id, blocked); err != nil {
		return err
	}
	if blocked {
		if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Блокировка строки пользователя согласует изменение баланса с блокировкой аккаунта.
func lockActiveUser(ctx context.Context, tx *sql.Tx, id int64) error {
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if u.Blocked {
		return domain.ErrBlocked
	}
	if u.Role != domain.RoleUser {
		return domain.ErrForbidden
	}
	return nil
}

const orderColumns = `id,user_id,number,status,external_status,accrual,reward_percent,uploaded_at`

func scanOrder(row scanner) (*domain.Order, error) {
	var o domain.Order
	err := row.Scan(&o.ID, &o.UserID, &o.Number, &o.Status, &o.ExternalStatus, &o.Accrual, &o.RewardPercent, &o.UploadedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrOrderNotFound
	}
	return &o, err
}
func (s *Store) GetOrder(ctx context.Context, number string) (*domain.Order, error) {
	return scanOrder(s.db.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE number=$1`, number))
}
func (s *Store) CreateOrder(ctx context.Context, userID int64, number, externalStatus string, rate int64) (*domain.Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockActiveUser(ctx, tx, userID); err != nil {
		return nil, err
	}
	o, err := scanOrder(tx.QueryRowContext(ctx, `INSERT INTO orders(user_id,number,external_status,reward_percent)
 VALUES($1,$2,$3,$4) ON CONFLICT(number) DO NOTHING RETURNING `+orderColumns, userID, number, externalStatus, rate))
	if errors.Is(err, domain.ErrOrderNotFound) {
		old, e := scanOrder(tx.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE number=$1`, number))
		if e != nil {
			return nil, e
		}
		if old.UserID == userID {
			return nil, domain.ErrOrderOwnedByUser
		}
		return nil, domain.ErrOrderExists
	}
	if err != nil {
		return nil, err
	}
	return o, tx.Commit()
}
func (s *Store) ListOrders(ctx context.Context, userID int64, limit, offset int) ([]domain.Order, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE ($1::bigint=0 OR user_id=$1)
 ORDER BY uploaded_at DESC,id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Order, 0)
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *o)
	}
	return result, rows.Err()
}

// По истечении срока захвата заказ снова доступен для обработки.
func (s *Store) ClaimOrders(ctx context.Context, limit int, lease time.Duration) ([]domain.Order, error) {
	rows, err := s.db.QueryContext(ctx, `WITH pending AS (
 SELECT o.id FROM orders o JOIN users u ON u.id=o.user_id
 WHERE o.status IN ('NEW','PROCESSING') AND o.next_attempt_at<=now() AND NOT u.blocked
 ORDER BY o.next_attempt_at,o.id LIMIT $1 FOR UPDATE OF o SKIP LOCKED)
 UPDATE orders o SET status='PROCESSING',next_attempt_at=now()+($2*interval '1 second')
 FROM pending p WHERE o.id=p.id RETURNING o.id,o.user_id,o.number,o.status,o.external_status,o.accrual,o.reward_percent,o.uploaded_at`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Order, 0)
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *o)
	}
	return result, rows.Err()
}
func (s *Store) RetryOrder(ctx context.Context, number, externalStatus string, delay time.Duration) error {
	_, err := s.db.ExecContext(ctx, `UPDATE orders SET next_attempt_at=now()+($2*interval '1 second'),
 external_status=CASE WHEN $3='' THEN external_status ELSE $3 END WHERE number=$1 AND status='PROCESSING'`, number, delay.Seconds(), externalStatus)
	return err
}

// FinalizeOrder атомарно сохраняет итоговый статус и начисление. Повтор не меняет баланс.
func (s *Store) FinalizeOrder(ctx context.Context, order domain.Order, status, externalStatus string, amount domain.Money) error {
	if (status != domain.OrderStatusProcessed && status != domain.OrderStatusInvalid) || amount < 0 || amount > domain.MaxAmount || (status == domain.OrderStatusInvalid && amount != 0) {
		return domain.ErrInvalidAmount
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockActiveUser(ctx, tx, order.UserID); err != nil {
		return err
	}
	stored, err := scanOrder(tx.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE number=$1 FOR UPDATE`, order.Number))
	if err != nil {
		return err
	}
	if stored.UserID != order.UserID {
		return domain.ErrOrderOwner
	}
	if stored.Status == domain.OrderStatusProcessed || stored.Status == domain.OrderStatusInvalid {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE orders SET status=$2,external_status=$3,accrual=$4 WHERE number=$1`, order.Number, status, externalStatus, amount); err != nil {
		return err
	}
	if status == domain.OrderStatusProcessed {
		if _, err = tx.ExecContext(ctx, `UPDATE balances SET current=current+$2 WHERE user_id=$1`, order.UserID, amount); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) GetBalance(ctx context.Context, userID int64) (domain.Balance, error) {
	var b domain.Balance
	err := s.db.QueryRowContext(ctx, `SELECT current,withdrawn FROM balances WHERE user_id=$1`, userID).Scan(&b.Current, &b.Withdrawn)
	if errors.Is(err, sql.ErrNoRows) {
		return b, domain.ErrUserNotFound
	}
	return b, err
}
func (s *Store) GetWithdrawal(ctx context.Context, number string) (*domain.Withdrawal, error) {
	var w domain.Withdrawal
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,order_number,sum,processed_at FROM withdrawals WHERE order_number=$1`, number).Scan(&w.ID, &w.UserID, &w.OrderNumber, &w.Sum, &w.ProcessedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &w, err
}
func (s *Store) Withdraw(ctx context.Context, userID int64, number string, sum domain.Money) error {
	if sum <= 0 || sum > domain.MaxAmount {
		return domain.ErrInvalidAmount
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockActiveUser(ctx, tx, userID); err != nil {
		return err
	}
	var owner int64
	var prior domain.Money
	err = tx.QueryRowContext(ctx, `SELECT user_id,sum FROM withdrawals WHERE order_number=$1`, number).Scan(&owner, &prior)
	if err == nil {
		if owner == userID && prior == sum {
			return nil
		}
		return domain.ErrWithdrawalConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE balances SET current=current-$2,withdrawn=withdrawn+$2 WHERE user_id=$1 AND current>=$2`, userID, sum)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrInsufficientFunds
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO withdrawals(user_id,order_number,sum) VALUES($1,$2,$3)`, userID, number, sum); err != nil {
		if uniqueViolation(err) {
			return domain.ErrWithdrawalConflict
		}
		return err
	}
	return tx.Commit()
}
func (s *Store) GetWithdrawals(ctx context.Context, userID int64, limit, offset int) ([]domain.Withdrawal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,order_number,sum,processed_at FROM withdrawals WHERE user_id=$1
 ORDER BY processed_at DESC,id DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Withdrawal, 0)
	for rows.Next() {
		var w domain.Withdrawal
		if err = rows.Scan(&w.ID, &w.UserID, &w.OrderNumber, &w.Sum, &w.ProcessedAt); err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, rows.Err()
}
func (s *Store) GetStats(ctx context.Context) (*domain.Stats, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stats := &domain.Stats{OrdersByStatus: map[string]int64{"NEW": 0, "PROCESSING": 0, "PROCESSED": 0, "INVALID": 0}}
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE blocked),now() FROM users`).Scan(&stats.UserCount, &stats.BlockedUsers, &stats.GeneratedAt)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT status,count(*),coalesce(sum(accrual),0) FROM orders GROUP BY status`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var count int64
		var amount domain.Money
		if err = rows.Scan(&status, &count, &amount); err != nil {
			rows.Close()
			return nil, err
		}
		stats.OrdersByStatus[status] = count
		stats.OrdersCount += count
		stats.TotalAccrued += amount
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(withdrawn),0) FROM balances`).Scan(&stats.TotalWithdrawn); err != nil {
		return nil, err
	}
	return stats, tx.Commit()
}
