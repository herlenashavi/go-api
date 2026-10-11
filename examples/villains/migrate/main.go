// Command migrate applies (or, with "down", rolls back) villains' SQL
// migrations in examples/villains/migrations against DATABASE_URL. See
// examples/heroes/migrate's identical doc comment for why this is its
// own binary rather than something examples/villains's service process
// does to itself on every boot.
//
//	DATABASE_URL=postgres://... go run ./examples/villains/migrate        # up
//	DATABASE_URL=postgres://... go run ./examples/villains/migrate down   # down
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/herlenashavi/go-api/examples/villains/migrations"
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
