package payment

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

type PostgresRepo struct {
	q *sqlc.Queries
}

func NewPostgresRepo(pool *pgxpool.Pool) *PostgresRepo {
	return &PostgresRepo{q: sqlc.New(pool)}
}

func (r *PostgresRepo) Save(ctx context.Context, p *Payment) error {
	if err := r.q.InsertPayment(ctx, paymentToInsertParams(p)); err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	return nil
}

func (r *PostgresRepo) Update(ctx context.Context, p *Payment) error {
	rows, err := r.q.UpdatePaymentStatus(ctx, paymentToUpdateStatusParams(p))
	if err != nil {
		return fmt.Errorf("update payment: %w", err)
	}
	if rows == 0 {
		return ErrPaymentNotFound
	}
	return nil
}

func (r *PostgresRepo) FindByID(ctx context.Context, id shared.PaymentID) (*Payment, error) {
	row, err := r.q.GetPaymentByID(ctx, postgres.UUIDToPg(uuid.UUID(id)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("query payment: %w", err)
	}
	return paymentFromSqlc(row)
}

func (r *PostgresRepo) FindByOrderID(ctx context.Context, orderID shared.OrderID) (*Payment, error) {
	row, err := r.q.GetLatestPaymentByOrder(ctx, postgres.UUIDToPg(uuid.UUID(orderID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("query payment by order: %w", err)
	}
	return paymentFromSqlc(row)
}
