package user

import (
	"github.com/google/uuid"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/postgres/sqlc"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

func userFromSqlc(r sqlc.User) *User {
	return rehydrate(
		shared.UserID(postgres.PgToUUID(r.ID)),
		r.Email,
		r.PasswordHash,
		r.DisplayName,
		postgres.PgToTime(r.CreatedAt),
		postgres.PgToTime(r.UpdatedAt),
	)
}

func userToInsertParams(u *User) sqlc.InsertUserParams {
	return sqlc.InsertUserParams{
		ID:           postgres.UUIDToPg(uuid.UUID(u.ID())),
		Email:        u.Email(),
		PasswordHash: u.PasswordHash(),
		DisplayName:  u.DisplayName(),
		CreatedAt:    postgres.TimeToPg(u.CreatedAt()),
		UpdatedAt:    postgres.TimeToPg(u.UpdatedAt()),
	}
}
