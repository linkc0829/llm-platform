-- name: InsertUser :exec
INSERT INTO users (id, email, password_hash, display_name, created_at, updated_at)
VALUES (
    sqlc.arg(id),
    sqlc.arg(email),
    sqlc.arg(password_hash),
    sqlc.arg(display_name),
    sqlc.arg(created_at),
    sqlc.arg(updated_at)
);

-- name: GetUserByID :one
SELECT id, email, password_hash, display_name, created_at, updated_at
FROM users
WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT id, email, password_hash, display_name, created_at, updated_at
FROM users
WHERE email = sqlc.arg(email);

-- name: UpdateUserDisplayName :exec
UPDATE users
SET display_name = sqlc.arg(display_name),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);
