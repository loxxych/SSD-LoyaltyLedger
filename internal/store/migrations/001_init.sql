CREATE TABLE users (
 id BIGSERIAL PRIMARY KEY,
 login TEXT NOT NULL UNIQUE CHECK (length(login) BETWEEN 3 AND 64),
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
 blocked BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE balances (
 user_id BIGINT PRIMARY KEY REFERENCES users(id),
 current BIGINT NOT NULL DEFAULT 0 CHECK (current >= 0),
 withdrawn BIGINT NOT NULL DEFAULT 0 CHECK (withdrawn >= 0)
);
CREATE TABLE sessions (
 token_hash CHAR(64) PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);
-- Записи обработки заказов в программе лояльности.
CREATE TABLE orders (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 number VARCHAR(32) NOT NULL UNIQUE,
 status TEXT NOT NULL DEFAULT 'NEW' CHECK (status IN ('NEW','PROCESSING','PROCESSED','INVALID')),
 external_status TEXT NOT NULL,
 accrual BIGINT NOT NULL DEFAULT 0 CHECK (accrual >= 0),
 reward_percent BIGINT NOT NULL CHECK (reward_percent BETWEEN 0 AND 100),
 uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (status = 'PROCESSED' OR accrual = 0)
);
CREATE INDEX orders_user_idx ON orders(user_id, uploaded_at DESC, id DESC);
CREATE INDEX orders_pending_idx ON orders(next_attempt_at, id) WHERE status IN ('NEW','PROCESSING');
CREATE TABLE withdrawals (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 order_number VARCHAR(32) NOT NULL UNIQUE,
 sum BIGINT NOT NULL CHECK (sum > 0),
 processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX withdrawals_user_idx ON withdrawals(user_id, processed_at DESC, id DESC);
