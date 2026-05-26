package shared

import (
	"errors"
	"fmt"
)

// Money represents an amount in the smallest currency unit (e.g. cents for USD,
// satoshi for BTC). Storing money as float64 is forbidden — use this type.
//
// Currency is ISO-4217 for fiat or the symbol for crypto (BTC, ETH, USDT).
type Money struct {
	amount   int64  // smallest unit
	currency string // ISO-4217 or crypto symbol
}

var (
	ErrInvalidAmount   = errors.New("amount must be non-negative")
	ErrCurrencyMismatch = errors.New("currency mismatch")
	ErrEmptyCurrency   = errors.New("currency must not be empty")
)

// NewMoney constructs a validated Money. Negative amounts are rejected; if you
// need a debit, use Subtract from a larger Money or model a separate type.
func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, ErrInvalidAmount
	}
	if currency == "" {
		return Money{}, ErrEmptyCurrency
	}
	return Money{amount: amount, currency: currency}, nil
}

// MustNewMoney panics on error. Use only in tests / constants.
func MustNewMoney(amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) IsZero() bool     { return m.amount == 0 }

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	if m.amount < other.amount {
		return Money{}, ErrInvalidAmount
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}

func (m Money) String() string {
	return fmt.Sprintf("%d %s", m.amount, m.currency)
}
