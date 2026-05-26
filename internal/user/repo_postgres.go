package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres/sqlc"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

type PostgresRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepo(pool *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{q: sqlc.New(pool)}
}

func (r *PostgresRepo) Save(ctx context.Context, u *User) error {
	if err := r.q.InsertUser(ctx, userToInsertParams(u)); err != nil {
		// 23505 = unique_violation; the `users.email` index is the canonical
		// uniqueness guarantee — the service pre-checks for friendly errors
		// but the unique index is the race-safe source of truth.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailAlreadyExists
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *PostgresRepo) FindByID(ctx context.Context, id shared.UserID) (*User, error) {
	row, err := r.q.GetUserByID(ctx, postgres.UUIDToPg(uuid.UUID(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user by id: %w", err)
	}
	return userFromSqlc(row), nil
}

func (r *PostgresRepo) FindByEmail(ctx context.Context, email string) (*User, error) {
	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user by email: %w", err)
	}
	return userFromSqlc(row), nil
}
