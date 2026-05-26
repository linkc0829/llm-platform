package postgres

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Helpers to convert between domain-friendly types (uuid.UUID, time.Time) and
// the pgtype.* values that sqlc-generated code uses. The conversions are
// deliberately "always valid" — domain entities never carry SQL NULL semantics;
// the absence of an entity is expressed at the repo boundary by returning a
// sentinel error, not by passing around nullable structs.
//
// If a feature needs NULL semantics for a specific column (e.g. an optional
// `deleted_at`), it should map that column through a pointer or sql.Null type
// at the feature's dto_internal.go boundary — not weaken the helpers here.

// UUIDToPg wraps a uuid.UUID as a non-null pgtype.UUID.
func UUIDToPg(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// PgToUUID unwraps a pgtype.UUID, returning uuid.Nil if the value is NULL.
func PgToUUID(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return uuid.UUID(p.Bytes)
}

// TimeToPg wraps a time.Time as a non-null pgtype.Timestamptz.
func TimeToPg(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// PgToTime unwraps a pgtype.Timestamptz, returning the zero time if NULL.
func PgToTime(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}
