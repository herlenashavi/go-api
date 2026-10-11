// Command migrate applies (or, with "down", rolls back) heroes' SQL
// migrations in examples/heroes/migrations against DATABASE_URL. It is
// deliberately its own binary, separate from examples/heroes's service
// process: running DDL is a one-off step a deploy pipeline (or, for this
// example, a Tilt local_resource — see examples/Tiltfile) takes before
// the service starts, not something the service does to itself on every
// boot, which would race across replicas and tie schema changes to
// pod restarts.
//
//	DATABASE_URL=postgres://... go run ./examples/heroes/migrate        # up
//	DATABASE_URL=postgres://... go run ./examples/heroes/migrate down   # down
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/herlenashavi/go-api/examples/heroes/migrations"
	"github.com/herlenashavi/go-api/pkg/dbmigrate"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "migrate: DATABASE_URL is required")
		os.Exit(1)
	}

	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}

	ctx := context.Background()
	var err error
	switch direction {
	case "up":
		err = dbmigrate.Up(ctx, dsn, migrations.FS)
	case "down":
		err = dbmigrate.Down(ctx, dsn, migrations.FS)
	default:
		fmt.Fprintf(os.Stderr, "migrate: unknown direction %q, want \"up\" or \"down\"\n", direction)
		os.Exit(1)
	}
	if err != nil {
		slog.Error("migrate: failed", "direction", direction, "error", err)
		os.Exit(1)
	}
	slog.Info("migrate: done", "direction", direction)
}
