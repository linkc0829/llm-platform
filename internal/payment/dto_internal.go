package payment

import (
	"github.com/google/uuid"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres/sqlc"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

func paymentFromSqlc(r sqlc.Payment) (*Payment, error) {
	amount, err := shared.NewMoney(r.Amount, r.Currency)
	if err != nil {
		return nil, err
	}
	return rehydrate(
		shared.PaymentID(postgres.PgToUUID(r.ID)),
		shared.UserID(postgres.PgToUUID(r.UserID)),
		shared.OrderID(postgres.PgToUUID(r.OrderID)),
		amount,
		Status(r.Status),
		postgres.PgToTime(r.CreatedAt),
		postgres.PgToTime(r.UpdatedAt),
	), nil
}

func paymentToInsertParams(p *Payment) sqlc.InsertPaymentParams {
	return sqlc.InsertPaymentParams{
		ID:        postgres.UUIDToPg(uuid.UUID(p.ID())),
		UserID:    postgres.UUIDToPg(uuid.UUID(p.UserID())),
		OrderID:   postgres.UUIDToPg(uuid.UUID(p.OrderID())),
		Amount:    p.Amount().Amount(),
		Currency:  p.Amount().Currency(),
		Status:    string(p.Status()),
		CreatedAt: postgres.TimeToPg(p.CreatedAt()),
		UpdatedAt: postgres.TimeToPg(p.UpdatedAt()),
	}
}

func paymentToUpdateStatusParams(p *Payment) sqlc.UpdatePaymentStatusParams {
	return sqlc.UpdatePaymentStatusParams{
		ID:        postgres.UUIDToPg(uuid.UUID(p.ID())),
		Status:    string(p.Status()),
		UpdatedAt: postgres.TimeToPg(p.UpdatedAt()),
	}
}
