package user

import "github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"

// ----------------------------------------------------------------------------
// Requests
// ----------------------------------------------------------------------------

type RegisterRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=8,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=64"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (r RegisterRequest) toInput() RegisterInput {
	return RegisterInput{
		Email:       r.Email,
		Password:    r.Password,
		DisplayName: r.DisplayName,
	}
}

func (r LoginRequest) toInput() LoginInput {
	return LoginInput{Email: r.Email, Password: r.Password}
}

// ----------------------------------------------------------------------------
// Responses
//
// Domain entities are NEVER returned directly (R6 — no marshalling domain).
// Always map through these DTOs.
// ----------------------------------------------------------------------------

type UserResponse struct {
	ID          shared.UserID `json:"id"`
	Email       string        `json:"email"`
	DisplayName string        `json:"display_name"`
	CreatedAt   string        `json:"created_at"`
}

type AuthResponse struct {
	User  UserResponse `json:"user"`
	Token string       `json:"token"`
}

func toUserResponse(u *User) UserResponse {
	return UserResponse{
		ID:          u.ID(),
		Email:       u.Email(),
		DisplayName: u.DisplayName(),
		CreatedAt:   u.CreatedAt().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toAuthResponse(u *User, token string) AuthResponse {
	return AuthResponse{User: toUserResponse(u), Token: token}
}
