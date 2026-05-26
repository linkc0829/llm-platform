-- Users -----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);

-- Orders ----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS orders (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount     BIGINT NOT NULL CHECK (amount > 0),
    currency   TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'canceled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id    ON orders (user_id);
CREATE INDEX IF NOT EXISTS idx_orders_user_created ON orders (user_id, created_at DESC);

-- Payments --------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payments (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id)  ON DELETE RESTRICT,
    order_id   UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    amount     BIGINT NOT NULL CHECK (amount > 0),
    currency   TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payments_order_id ON payments (order_id);
CREATE INDEX IF NOT EXISTS idx_payments_user_id  ON payments (user_id);
