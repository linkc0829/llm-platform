package order

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres/sqlc"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// PostgresRepo implements Repo via sqlc-generated queries. All SQL lives in
// sql/queries/order.sql and is regenerated with `make sqlc-generate`. The
// only Go code that talks to pgx directly is the platform layer; this file
// only orchestrates conversions between domain types and sqlc-generated row
// structs.
type PostgresRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepo(pool *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{q: sqlc.New(pool)}
}

func (r *PostgresRepo) Save(ctx context.Context, o *Order) error {
	if err := r.q.InsertOrder(ctx, orderToInsertParams(o)); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func (r *PostgresRepo) Update(ctx context.Context, o *Order) error {
	rows, err := r.q.UpdateOrderStatus(ctx, orderToUpdateStatusParams(o))
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	if rows == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func (r *PostgresRepo) FindByID(ctx context.Context, id shared.OrderID) (*Order, error) {
	row, err := r.q.GetOrderByID(ctx, postgres.UUIDToPg(uuid.UUID(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("query order: %w", err)
	}
	return orderFromSqlc(row)
}

func (r *PostgresRepo) ListByUser(ctx context.Context, userID shared.UserID, p shared.Pagination) ([]*Order, int64, error) {
	rows, err := r.q.ListOrdersByUser(ctx, sqlc.ListOrdersByUserParams{
		UserID:     postgres.UUIDToPg(uuid.UUID(userID)),
		PageLimit:  int32(p.Limit),
		PageOffset: int32(p.Offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}

	out := make([]*Order, 0, len(rows))
	for _, row := range rows {
		o, err := orderFromSqlc(row)
		if err != nil {
			return nil, 0, fmt.Errorf("rehydrate order: %w", err)
		}
		out = append(out, o)
	}

	total, err := r.q.CountOrdersByUser(ctx, postgres.UUIDToPg(uuid.UUID(userID)))
	if err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	return out, total, nil
}
