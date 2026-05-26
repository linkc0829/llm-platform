package user

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
)

// ----------------------------------------------------------------------------
// Hand-rolled fakes (no gomock dependency — keeps tests readable and lets you
// see exactly what the test sets up). For larger interfaces, switch to gomock.
// ----------------------------------------------------------------------------

type fakeRepo struct {
	saveErr        error
	findByIDUser   *User
	findByIDErr    error
	findByEmailErr error
	findByEmailRet *User
	saveCalls      int
}

func (f *fakeRepo) Save(_ context.Context, u *User) error {
	f.saveCalls++
	return f.saveErr
}
func (f *fakeRepo) FindByID(_ context.Context, _ shared.UserID) (*User, error) {
	return f.findByIDUser, f.findByIDErr
}
func (f *fakeRepo) FindByEmail(_ context.Context, _ string) (*User, error) {
	return f.findByEmailRet, f.findByEmailErr
}

type fakeHasher struct {
	hashOut    string
	hashErr    error
	compareErr error
}

func (f *fakeHasher) Hash(_ string) (string, error) { return f.hashOut, f.hashErr }
func (f *fakeHasher) Compare(_, _ string) error     { return f.compareErr }

type fakeTokens struct {
	out string
	err error
}

func (f *fakeTokens) Issue(_ string) (string, error) { return f.out, f.err }

// ----------------------------------------------------------------------------
// Register
// ----------------------------------------------------------------------------

func TestService_Register(t *testing.T) {
	tests := []struct {
		name        string
		input       RegisterInput
		setup       func(*fakeRepo, *fakeHasher, *fakeTokens)
		wantErr     error
		wantToken   string
		wantSaveHit int
	}{
		{
			name:  "happy_path",
			input: RegisterInput{Email: "alice@example.com", Password: "pa55word!", DisplayName: "Alice"},
			setup: func(r *fakeRepo, h *fakeHasher, tk *fakeTokens) {
				r.findByEmailErr = ErrUserNotFound
				h.hashOut = "hashed"
				tk.out = "tok"
			},
			wantToken:   "tok",
			wantSaveHit: 1,
		},
		{
			name:  "email_already_exists",
			input: RegisterInput{Email: "alice@example.com", Password: "pa55word!", DisplayName: "Alice"},
			setup: func(r *fakeRepo, h *fakeHasher, _ *fakeTokens) {
				existing, _ := NewUser("alice@example.com", "h", "Alice")
				r.findByEmailRet = existing
			},
			wantErr: ErrEmailAlreadyExists,
		},
		{
			name:  "invalid_email_rejected_by_domain",
			input: RegisterInput{Email: "not-an-email", Password: "pa55word!", DisplayName: "Bob"},
			setup: func(r *fakeRepo, h *fakeHasher, _ *fakeTokens) {
				r.findByEmailErr = ErrUserNotFound
				h.hashOut = "hashed"
			},
			wantErr: ErrInvalidEmail,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			hasher := &fakeHasher{}
			tokens := &fakeTokens{}
			tt.setup(repo, hasher, tokens)

			svc := NewService(repo, hasher, tokens)
			u, tok, err := svc.Register(context.Background(), tt.input)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "want %v, got %v", tt.wantErr, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, u)
			assert.Equal(t, tt.wantToken, tok)
			assert.Equal(t, tt.wantSaveHit, repo.saveCalls)
		})
	}
}

// ----------------------------------------------------------------------------
// Login
// ----------------------------------------------------------------------------

func TestService_Login(t *testing.T) {
	existing, _ := NewUser("alice@example.com", "hashed", "Alice")

	tests := []struct {
		name      string
		input     LoginInput
		setup     func(*fakeRepo, *fakeHasher, *fakeTokens)
		wantErr   error
		wantToken string
	}{
		{
			name:  "happy_path",
			input: LoginInput{Email: "alice@example.com", Password: "pa55word!"},
			setup: func(r *fakeRepo, _ *fakeHasher, tk *fakeTokens) {
				r.findByEmailRet = existing
				tk.out = "tok"
			},
			wantToken: "tok",
		},
		{
			name:  "wrong_password",
			input: LoginInput{Email: "alice@example.com", Password: "wrong"},
			setup: func(r *fakeRepo, h *fakeHasher, _ *fakeTokens) {
				r.findByEmailRet = existing
				h.compareErr = ErrInvalidCredentials
			},
			wantErr: ErrInvalidCredentials,
		},
		{
			name:  "user_not_found_returns_invalid_credentials",
			input: LoginInput{Email: "nobody@example.com", Password: "x"},
			setup: func(r *fakeRepo, _ *fakeHasher, _ *fakeTokens) {
				r.findByEmailErr = ErrUserNotFound
			},
			wantErr: ErrInvalidCredentials,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			hasher := &fakeHasher{}
			tokens := &fakeTokens{}
			tt.setup(repo, hasher, tokens)

			svc := NewService(repo, hasher, tokens)
			_, tok, err := svc.Login(context.Background(), tt.input)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, tok)
		})
	}
}
