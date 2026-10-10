// Package dbmigrate applies golang-migrate migrations embedded in a
// service's binary against that service's Postgres database. It is
// deliberately separate from pkg/dbx: dbx owns the long-lived connection
// pool a running service queries through, while dbmigrate opens its own
// short-lived database/sql connection — golang-migrate's Postgres driver
// speaks database/sql, not pgx's native pool interface — runs to
// completion, and closes it. Nothing here is shared with dbx.Pool.
package dbmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Up applies every pending migration in migrations — typically an
// embed.FS of a service's migrations/*.sql files — against dsn, up to
// the latest version. Returns nil if the schema was already current.
func Up(ctx context.Context, dsn string, migrations fs.FS) error {
	return withMigrate(ctx, dsn, migrations, func(m *migrate.Migrate) error {
		return ignoreNoChange(m.Up())
	})
}

// Down rolls back every applied migration. Intended for local dev and
// tests — a production rollback is a decision to make by hand, one
// migration at a time, not a default this package should make easy.
func Down(ctx context.Context, dsn string, migrations fs.FS) error {
	return withMigrate(ctx, dsn, migrations, func(m *migrate.Migrate) error {
		return ignoreNoChange(m.Down())
	})
}

func ignoreNoChange(err error) error {
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

func withMigrate(ctx context.Context, dsn string, migrations fs.FS, fn func(*migrate.Migrate) error) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("dbmigrate: open: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("dbmigrate: ping: %w", err)
	}

	source, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("dbmigrate: source: %w", err)
	}

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("dbmigrate: driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx", driver)
	if err != nil {
		return fmt.Errorf("dbmigrate: migrate: %w", err)
	}

	if err := fn(m); err != nil {
		return fmt.Errorf("dbmigrate: %w", err)
	}
	return nil
}
