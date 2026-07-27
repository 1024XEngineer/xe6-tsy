//go:build integration

package recordstore

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const recordStoreTestDatabaseURL = "RECORDSTORE_TEST_DATABASE_URL"

func testDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(recordStoreTestDatabaseURL)
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", recordStoreTestDatabaseURL)
	}

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", recordStoreTestDatabaseURL, err)
	}
	if !strings.HasSuffix(strings.ToLower(adminConfig.ConnConfig.Database), "_test") {
		t.Fatalf("%s must target a dedicated database ending in _test, got %q", recordStoreTestDatabaseURL, adminConfig.ConnConfig.Database)
	}
	adminConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := testSchemaName(t)
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+quoteIdentifier(schema)); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoteIdentifier(schema)+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse isolated integration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET search_path TO "+quoteIdentifier(schema))
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL integration pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("create random integration schema name: %v", err)
	}
	return fmt.Sprintf("recordstore_%x", randomBytes)
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
