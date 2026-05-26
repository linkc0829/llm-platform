package payment

import "errors"

var (
	ErrPaymentNotFound = errors.New("payment not found")
	ErrInvalidInput    = errors.New("invalid payment input")
	ErrInvalidAmount   = errors.New("invalid payment amount")
	ErrGatewayDeclined = errors.New("payment gateway declined")
	ErrGatewayTimeout  = errors.New("payment gateway timeout")
)
