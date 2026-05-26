package order

import (
	"github.com/google/uuid"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres/sqlc"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// orderFromSqlc converts a generated sqlc row into a domain Order, rehydrating
// the value objects (Money, Status) and re-checking nothing — rehydrate is for
// trusted storage data, not for validation.
func orderFromSqlc(r sqlc.Order) (*Order, error) {
	amount, err := shared.NewMoney(r.Amount, r.Currency)
	if err != nil {
		return nil, err
	}
	return rehydrate(
		shared.OrderID(postgres.PgToUUID(r.ID)),
		shared.UserID(postgres.PgToUUID(r.UserID)),
		amount,
		Status(r.Status),
		postgres.PgToTime(r.CreatedAt),
		postgres.PgToTime(r.UpdatedAt),
	), nil
}

func orderToInsertParams(o *Order) sqlc.InsertOrderParams {
	return sqlc.InsertOrderParams{
		ID:        postgres.UUIDToPg(uuid.UUID(o.ID())),
		UserID:    postgres.UUIDToPg(uuid.UUID(o.UserID())),
		Amount:    o.Amount().Amount(),
		Currency:  o.Amount().Currency(),
		Status:    string(o.Status()),
		CreatedAt: postgres.TimeToPg(o.CreatedAt()),
		UpdatedAt: postgres.TimeToPg(o.UpdatedAt()),
	}
}

func orderToUpdateStatusParams(o *Order) sqlc.UpdateOrderStatusParams {
	return sqlc.UpdateOrderStatusParams{
		ID:        postgres.UUIDToPg(uuid.UUID(o.ID())),
		Status:    string(o.Status()),
		UpdatedAt: postgres.TimeToPg(o.UpdatedAt()),
	}
}
