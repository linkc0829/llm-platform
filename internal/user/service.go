package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// Service holds the use cases for the user feature.
type Service struct {
	repo   Repo
	hasher Hasher
	tokens TokenIssuer
}

// NewService takes only interfaces — never concrete adapters (R3.4).
func NewService(repo Repo, hasher Hasher, tokens TokenIssuer) *Service {
	return &Service{repo: repo, hasher: hasher, tokens: tokens}
}

// RegisterInput captures input to RegisterUser. Defined here (not dto_http) so
// non-HTTP callers (e.g. CLI seeders) can use the service without HTTP types.
type RegisterInput struct {
	Email       string
	Password    string
	DisplayName string
}

// Register creates a new user. Returns the user and an auth token.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*User, string, error) {
	// Pre-check duplicate. Race-condition safety relies on a unique index in PG.
	existing, err := s.repo.FindByEmail(ctx, in.Email)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, "", fmt.Errorf("find by email: %w", err)
	}
	if existing != nil {
		return nil, "", ErrEmailAlreadyExists
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, "", fmt.Errorf("hash password: %w", err)
	}

	u, err := NewUser(in.Email, hash, in.DisplayName)
	if err != nil {
		return nil, "", err
	}

	if err := s.repo.Save(ctx, u); err != nil {
		return nil, "", fmt.Errorf("save user: %w", err)
	}

	token, err := s.tokens.Issue(u.ID().String())
	if err != nil {
		return nil, "", fmt.Errorf("issue token: %w", err)
	}
	return u, token, nil
}

// LoginInput captures login credentials.
type LoginInput struct {
	Email    string
	Password string
}

// Login authenticates a user and returns an auth token.
func (s *Service) Login(ctx context.Context, in LoginInput) (*User, string, error) {
	u, err := s.repo.FindByEmail(ctx, in.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", fmt.Errorf("find by email: %w", err)
	}
	if err := s.hasher.Compare(u.PasswordHash(), in.Password); err != nil {
		return nil, "", ErrInvalidCredentials
	}
	token, err := s.tokens.Issue(u.ID().String())
	if err != nil {
		return nil, "", fmt.Errorf("issue token: %w", err)
	}
	return u, token, nil
}

// GetByID is used by other features (e.g. order) via the UserLookup port.
func (s *Service) GetByID(ctx context.Context, id shared.UserID) (*User, error) {
	return s.repo.FindByID(ctx, id)
}

// ----------------------------------------------------------------------------
// Cross-feature contract
//
// Other features (order, payment) need to look up users. They must NOT import
// this package. Instead, they declare a UserLookup interface in their own
// ports.go that *Service structurally satisfies via its GetByID method.
// ----------------------------------------------------------------------------
