// Package shared holds zero-dependency value objects used across features.
//
// RULE: This package must not import any internal/platform/* or third-party
// driver. Stdlib only (and trivially-pure libs like google/uuid).
package shared

import (
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ----------------------------------------------------------------------------
// Strongly-typed IDs
//
// Each ID type implements:
//   - encoding.TextMarshaler / TextUnmarshaler (for JSON)
//   - sql.Scanner / driver.Valuer            (for database/sql + pgx fallback)
//
// pgx's native codec works on `uuid.UUID` directly but does NOT auto-promote to
// named types defined as `type X uuid.UUID`. The Scan/Value pair makes pgx fall
// back to the database/sql interface and round-trip our IDs cleanly.
// ----------------------------------------------------------------------------

type UserID uuid.UUID
type OrderID uuid.UUID
type PaymentID uuid.UUID

func NewUserID() UserID       { return UserID(uuid.New()) }
func NewOrderID() OrderID     { return OrderID(uuid.New()) }
func NewPaymentID() PaymentID { return PaymentID(uuid.New()) }

func (id UserID) String() string    { return uuid.UUID(id).String() }
func (id OrderID) String() string   { return uuid.UUID(id).String() }
func (id PaymentID) String() string { return uuid.UUID(id).String() }

func (id UserID) IsZero() bool    { return uuid.UUID(id) == uuid.Nil }
func (id OrderID) IsZero() bool   { return uuid.UUID(id) == uuid.Nil }
func (id PaymentID) IsZero() bool { return uuid.UUID(id) == uuid.Nil }

// ---------- JSON / text ----------

func (id UserID) MarshalText() ([]byte, error)    { return uuid.UUID(id).MarshalText() }
func (id OrderID) MarshalText() ([]byte, error)   { return uuid.UUID(id).MarshalText() }
func (id PaymentID) MarshalText() ([]byte, error) { return uuid.UUID(id).MarshalText() }

func (id *UserID) UnmarshalText(b []byte) error {
	u, err := uuid.ParseBytes(b)
	if err != nil {
		return err
	}
	*id = UserID(u)
	return nil
}

func (id *OrderID) UnmarshalText(b []byte) error {
	u, err := uuid.ParseBytes(b)
	if err != nil {
		return err
	}
	*id = OrderID(u)
	return nil
}

func (id *PaymentID) UnmarshalText(b []byte) error {
	u, err := uuid.ParseBytes(b)
	if err != nil {
		return err
	}
	*id = PaymentID(u)
	return nil
}

// ---------- database/sql round-tripping ----------

func (id UserID) Value() (driver.Value, error)    { return uuid.UUID(id).Value() }
func (id OrderID) Value() (driver.Value, error)   { return uuid.UUID(id).Value() }
func (id PaymentID) Value() (driver.Value, error) { return uuid.UUID(id).Value() }

func (id *UserID) Scan(src any) error {
	var u uuid.UUID
	if err := u.Scan(src); err != nil {
		return fmt.Errorf("scan UserID: %w", err)
	}
	*id = UserID(u)
	return nil
}

func (id *OrderID) Scan(src any) error {
	var u uuid.UUID
	if err := u.Scan(src); err != nil {
		return fmt.Errorf("scan OrderID: %w", err)
	}
	*id = OrderID(u)
	return nil
}

func (id *PaymentID) Scan(src any) error {
	var u uuid.UUID
	if err := u.Scan(src); err != nil {
		return fmt.Errorf("scan PaymentID: %w", err)
	}
	*id = PaymentID(u)
	return nil
}

// ---------- Parsers ----------

var ErrInvalidID = errors.New("invalid id")

func ParseUserID(s string) (UserID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return UserID{}, ErrInvalidID
	}
	return UserID(u), nil
}

func ParseOrderID(s string) (OrderID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return OrderID{}, ErrInvalidID
	}
	return OrderID(u), nil
}

func ParsePaymentID(s string) (PaymentID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return PaymentID{}, ErrInvalidID
	}
	return PaymentID(u), nil
}
