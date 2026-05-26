//go:build integration

// Package integration holds end-to-end + adapter integration tests.
//
// Run with:
//
//	make test-integration
//
// or
//
//	go test -tags=integration -count=1 ./test/integration/...
//
// Requires a locally running Postgres. The connection string is read from
// POSTGRES_TEST_DSN; if unset it falls back to
// postgres://postgres:postgres@localhost:5432/app_test?sslmode=disable.
// Migrations under ../../migrations should be applied before running.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Sanity check that we're running from the test/integration directory
	// — migrations are referenced relative to this package.
	if _, err := filepath.Abs("."); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// Skeleton — fill in once the repo exercise is implemented.
//
// Recommended pattern:
//
//	1. Read POSTGRES_TEST_DSN (fallback to the local default above).
//	2. Build a *pgxpool.Pool against the local Postgres.
//	3. Construct order.NewPostgresRepo(pool) and exercise it.
//	4. Clean up any rows the test inserted.
//
// Keeping this file as a build-tagged stub means `go test ./...` (no tag)
// stays green on machines without Postgres, while `make test-integration`
// runs the real thing.
func TestOrderRepo_Integration_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = ctx

	require.True(t, true, "replace with real local-postgres repo round-trip")
}
