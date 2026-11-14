package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateGrantsReaderIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; database integration unverified")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Close()
	schema := "vhness_grant_test_" + time.Now().UTC().Format("20060102150405")
	reader := schema + "_reader"
	schemaSQL, readerSQL := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{reader}.Sanitize()
	// Like public, the schema is usable by the reader; only table privileges differ.
	if _, err = admin.Pool.Exec(ctx, "CREATE SCHEMA "+schemaSQL+"; CREATE ROLE "+readerSQL+" NOLOGIN; GRANT USAGE ON SCHEMA "+schemaSQL+" TO "+readerSQL); err != nil {
		t.Fatal(err)
	}
	defer admin.Pool.Exec(context.Background(), "DROP SCHEMA "+schemaSQL+" CASCADE; DROP ROLE "+readerSQL)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := &DB{Pool: pool}
	dir := t.TempDir()
	write := func(name, sql string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0600); err != nil {
			t.Fatal(err)
		}
	}
	readAsReader := func(table string) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+readerSQL); err != nil {
			t.Fatal(err)
		}
		var n int
		return tx.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&n)
	}

	write("0001_first.sql", "CREATE TABLE first_listing (id text PRIMARY KEY); INSERT INTO first_listing VALUES ('a');")
	if err = d.Migrate(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err = readAsReader("first_listing"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("migrate without a reader role changed privileges: %v", err)
	}

	write("0002_second.sql", "CREATE TABLE second_listing (id text PRIMARY KEY); INSERT INTO second_listing VALUES ('b');")
	for _, bad := range []string{" padded", strings.Repeat("r", 64), "missing_reader_role_" + schema} {
		if d.MigrateGranting(ctx, dir, bad) == nil {
			t.Fatalf("reader role %q accepted", bad)
		}
	}
	var applied int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM schema_versions").Scan(&applied); err != nil || applied != 1 {
		t.Fatalf("rejected reader role still applied migrations: %d %v", applied, err)
	}

	if err = d.MigrateGranting(ctx, dir, reader); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"first_listing", "second_listing", "schema_versions"} {
		if err = readAsReader(table); err != nil {
			t.Fatalf("reader cannot read %s after migrate: %v", table, err)
		}
	}
	if err = d.MigrateGranting(ctx, dir, reader); err != nil {
		t.Fatal("repeat granting migration", err)
	}
}
