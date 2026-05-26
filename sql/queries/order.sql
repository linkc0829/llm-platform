-- Convention: use sqlc.arg(<name>) for every parameter, not positional $N.
-- Keep arg names matching column names so generated Go field names stay
-- predictable (e.g. sqlc.arg(user_id) → UserID).

-- name: InsertOrder :exec
INSERT INTO orders (id, user_id, amount, currency, status, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(user_id),
    sqlc.arg(amount),
    sqlc.arg(currency),
    sqlc.arg(status),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: UpdateOrderStatus :execrows
UPDATE orders
SET status     = sqlc.arg(status),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GetOrderByID :one
SELECT id, user_id, amount, currency, status, created_at, updated_at
FROM orders
WHERE id = sqlc.arg(id);

-- name: ListOrdersByUser :many
SELECT id, user_id, amount, currency, status, created_at, updated_at
FROM orders
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountOrdersByUser :one
SELECT COUNT(*) FROM orders WHERE user_id = sqlc.arg(user_id);
