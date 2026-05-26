// Package user is a feature slice for user management & authentication.
//
// Layers (within this package):
//   - domain.go        : entity, value objects, invariants
//   - service.go       : use cases (Register, Login, GetProfile)
//   - ports.go         : outbound interfaces (repo)
//   - errors.go        : sentinel errors
//   - dto_http.go      : HTTP request/response shapes
//   - dto_internal.go  : persistence row + cross-feature DTOs
//   - handler_http.go  : Gin handlers
//   - routes.go        : route registration
//   - repo_postgres.go : pgx adapter
package user

import (
	"strings"
	"time"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// User is the aggregate root for this feature.
//
// Invariants enforced via constructor:
//   - Email is non-empty, lowercase, contains @
//   - PasswordHash is non-empty (callers hash before constructing)
type User struct {
	id           shared.UserID
	email        string
	passwordHash string
	displayName  string
	createdAt    time.Time
	updatedAt    time.Time
}

// NewUser constructs a validated User. Use this in registration flows.
func NewUser(email, passwordHash, displayName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, ErrInvalidEmail
	}
	if passwordHash == "" {
		return nil, ErrInvalidPassword
	}
	now := time.Now().UTC()
	return &User{
		id:           shared.NewUserID(),
		email:        email,
		passwordHash: passwordHash,
		displayName:  strings.TrimSpace(displayName),
		createdAt:    now,
		updatedAt:    now,
	}, nil
}

// rehydrate reconstructs an entity from persistence without re-validating.
// Only the repo should call this.
func rehydrate(id shared.UserID, email, passwordHash, displayName string, createdAt, updatedAt time.Time) *User {
	return &User{
		id:           id,
		email:        email,
		passwordHash: passwordHash,
		displayName:  displayName,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
	}
}

// Accessors (entities expose getters, not fields, to preserve invariants).

func (u *User) ID() shared.UserID    { return u.id }
func (u *User) Email() string        { return u.email }
func (u *User) PasswordHash() string { return u.passwordHash }
func (u *User) DisplayName() string  { return u.displayName }
func (u *User) CreatedAt() time.Time { return u.createdAt }
func (u *User) UpdatedAt() time.Time { return u.updatedAt }

// Rename mutates the display name with validation.
func (u *User) Rename(newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return ErrInvalidDisplayName
	}
	u.displayName = newName
	u.updatedAt = time.Now().UTC()
	return nil
}
