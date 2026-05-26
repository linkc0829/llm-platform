package order

import "errors"

var (
	ErrOrderNotFound          = errors.New("order not found")
	ErrInvalidUserID          = errors.New("invalid user id")
	ErrInvalidAmount          = errors.New("invalid amount")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrPaymentFailed          = errors.New("payment failed")
	ErrUserNotFound           = errors.New("user not found")
)
