package user

import "errors"

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrInvalidPassword    = errors.New("invalid password")
	ErrInvalidDisplayName = errors.New("invalid display name")
	ErrInvalidCredentials = errors.New("invalid credentials")
)
