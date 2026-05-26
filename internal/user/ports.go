package user

import (
	"context"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// Repo is the outbound port for user persistence.
//
//go:generate mockgen -source=ports.go -destination=mocks/mock_ports.go -package=mocks
type Repo interface {
	Save(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id shared.UserID) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
}

// Hasher is the outbound port for password hashing. Decoupling lets us swap
// bcrypt for argon2 without touching the service.
type Hasher interface {
	Hash(plain string) (string, error)
	Compare(hashed, plain string) error
}

// TokenIssuer is the outbound port for issuing auth tokens. The platform/auth
// package satisfies this structurally — the user package never imports it.
type TokenIssuer interface {
	Issue(subject string) (string, error)
}
