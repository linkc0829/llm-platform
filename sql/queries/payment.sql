-- name: InsertPayment :exec
INSERT INTO payments (id, user_id, order_id, amount, currency, status, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    sqlc.arg(order_id),
    sqlc.arg(amount),
    sqlc.arg(currency),
    sqlc.arg(status),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: UpdatePaymentStatus :execrows
UPDATE payments
SET status     = sqlc.arg(status),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GetPaymentByID :one
SELECT id, user_id, order_id, amount, currency, status, created_at, updated_at
FROM payments
WHERE id = sqlc.arg(id);

-- name: GetLatestPaymentByOrder :one
SELECT id, user_id, order_id, amount, currency, status, created_at, updated_at
FROM payments
WHERE order_id = sqlc.arg(order_id)
ORDER BY created_at DESC
LIMIT 1;
