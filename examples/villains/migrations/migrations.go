// Package migrations embeds villains' own SQL migration files into
// whatever binary imports it — see examples/heroes/migrations for the
// identical pattern applied to heroes' schema instead. examples/villains/migrate
// is the command that actually runs these, via pkg/dbmigrate.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
