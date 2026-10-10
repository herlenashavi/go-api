// Package migrations embeds heroes' own SQL migration files into
// whatever binary imports it, so applying them needs nothing more than
// this package and a DATABASE_URL — no migrations/ directory has to
// exist on disk next to the binary. See examples/heroes/migrate for the
// command that actually runs them, via pkg/dbmigrate.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
