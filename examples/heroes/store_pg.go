// store_pg.go shows the dbx/pgx-backed shape a real Store implementation
// takes. main.go uses it when DATABASE_URL is set and falls back to
// MemoryHeroStore otherwise, so `go run .` still needs no database. Both
// satisfy crud.Store (and heroStore) identically — which one main.go
// constructs is the only difference between the two modes.
//
// A real service would generate the query layer with sqlc from SQL like:
//
//	-- name: GetHero :one
//	SELECT id, name, super_power, status, created_at, modified_at
//	FROM heroes WHERE id = $1;
//
// PGHeroStore hand-writes the equivalent queries instead, so this file has
// no sqlc-generated code to keep in sync with — in a real service, replace
// the pool.QueryRow/Exec calls below with sqlc's generated methods.
//
// Filtering never interpolates a field name into SQL text: filterColumns
// maps each allowlisted crud.Filter.Field to its column, so even a Store
// that (wrongly) trusted an un-validated field name couldn't be used to
// probe arbitrary columns.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/dbx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

type PGHeroStore struct {
	pool *dbx.Pool
}

func NewPGHeroStore(pool *dbx.Pool) *PGHeroStore {
	return &PGHeroStore{pool: pool}
}

// Compile-time proof this satisfies the same interface MemoryHeroStore
// does — if either store's method set drifts from crud.Store, this line
// fails to build instead of main.go failing to build only when you
// actually try the swap.
var _ crud.Store[Hero, CreateHero, UpdateHero] = (*PGHeroStore)(nil)

const heroColumns = "id, name, super_power, status, nemesis_villain_id, created_at, modified_at"

func scanHero(row pgx.Row) (Hero, error) {
	var h Hero
	var nemesisVillainID *string
	err := row.Scan(&h.ID, &h.Name, &h.SuperPower, &h.Status, &nemesisVillainID, &h.CreatedAt, &h.ModifiedAt)
	if nemesisVillainID != nil {
		h.NemesisVillainID = *nemesisVillainID
	}
	return h, err
}

// parseHeroID rejects a non-UUID id before it reaches Postgres. Sent as-is,
// Postgres refuses to cast it to the uuid column and the query fails with
// a server error — which would surface as a 500 for what is really a
// malformed request. 400 matches MemoryHeroStore's behavior.
func parseHeroID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, problem.BadRequest("id is not a valid UUID")
	}
	return parsed, nil
}

func (s *PGHeroStore) Get(ctx context.Context, id string) (Hero, error) {
	parsed, err := parseHeroID(id)
	if err != nil {
		return Hero{}, err
	}
	h, err := scanHero(s.pool.QueryRow(ctx, `SELECT `+heroColumns+` FROM heroes WHERE id = $1`, parsed.String()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Hero{}, problem.NotFound("Hero")
		}
		return Hero{}, fmt.Errorf("get hero: %w", err)
	}
	return h, nil
}

func (s *PGHeroStore) Create(ctx context.Context, in CreateHero) (Hero, error) {
	status := in.Status
	if status == "" {
		status = HeroStatusActive
	}
	// A single INSERT is already atomic in Postgres — no dbx.WithTx
	// needed here. See CreateWithAudit below for when you do need one.
	h, err := scanHero(s.pool.QueryRow(ctx,
		`INSERT INTO heroes (id, name, super_power, status, nemesis_villain_id)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4)
		 RETURNING `+heroColumns,
		in.Name, in.SuperPower, status, nullIfEmpty(in.NemesisVillainID),
	))
	if err != nil {
		return Hero{}, fmt.Errorf("create hero: %w", err)
	}
	return h, nil
}

// CreateWithAudit demonstrates dbx.WithTx: the insert and an audit-log row
// must succeed or fail together, which is exactly what a bare *pgxpool.Pool
// doing one statement after another cannot guarantee. Not part of
// crud.Store — this is the kind of extra method a Store concrete type is
// free to carry for handlers that need the atomicity without exposing it
// through the generic interface.
func (s *PGHeroStore) CreateWithAudit(ctx context.Context, in CreateHero, actor string) (Hero, error) {
	var h Hero
	err := dbx.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		h, err = scanHero(tx.QueryRow(ctx,
			`INSERT INTO heroes (id, name, super_power, status)
			 VALUES (gen_random_uuid(), $1, $2, 'active')
			 RETURNING `+heroColumns,
			in.Name, in.SuperPower,
		))
		if err != nil {
			return fmt.Errorf("insert hero: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO hero_audit_log (hero_id, actor, event) VALUES ($1, $2, 'created')`,
			h.ID, actor,
		); err != nil {
			return fmt.Errorf("insert audit row: %w", err) // rolls back the insert above too
		}
		return nil
	})
	return h, err
}

func (s *PGHeroStore) Update(ctx context.Context, id string, in UpdateHero) (Hero, error) {
	parsed, err := parseHeroID(id)
	if err != nil {
		return Hero{}, err
	}
	h, err := scanHero(s.pool.QueryRow(ctx,
		`UPDATE heroes SET
		   name = COALESCE($2, name),
		   super_power = COALESCE($3, super_power),
		   status = COALESCE($4, status),
		   nemesis_villain_id = COALESCE($5, nemesis_villain_id),
		   modified_at = now()
		 WHERE id = $1
		 RETURNING `+heroColumns,
		parsed.String(), in.Name, in.SuperPower, in.Status, in.NemesisVillainID,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Hero{}, problem.NotFound("Hero")
		}
		return Hero{}, fmt.Errorf("update hero: %w", err)
	}
	return h, nil
}

func (s *PGHeroStore) Delete(ctx context.Context, id string) error {
	parsed, err := parseHeroID(id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM heroes WHERE id = $1`, parsed.String())
	if err != nil {
		return fmt.Errorf("delete hero: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return problem.NotFound("Hero")
	}
	return nil
}

// Retire sets a hero's status to retired. It isn't part of crud.Store —
// see MemoryHeroStore.Retire's doc comment — but heroStore (retire.go)
// requires it of every backend main.go can construct, PGHeroStore
// included.
func (s *PGHeroStore) Retire(ctx context.Context, id uuid.UUID) (Hero, error) {
	h, err := scanHero(s.pool.QueryRow(ctx,
		`UPDATE heroes SET status = 'retired', modified_at = now()
		 WHERE id = $1
		 RETURNING `+heroColumns,
		id.String(),
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Hero{}, problem.NotFound("Hero")
		}
		return Hero{}, fmt.Errorf("retire hero: %w", err)
	}
	return h, nil
}

// filterColumns maps each FilterableFields key (set in main.go's
// crud.Config) to its actual column — the allowlist that keeps filtering
// from becoming "any column the client names."
var filterColumns = map[string]string{
	"status": "status",
	"name":   "name",
}

func (s *PGHeroStore) List(ctx context.Context, params crud.ListParams) (crud.ListResult[Hero], error) {
	where, args, err := buildWhere(params.Filters)
	if err != nil {
		return crud.ListResult[Hero]{}, err
	}
	orderBy := buildOrderBy(params.Sort)

	query := `SELECT ` + heroColumns + ` FROM heroes` + where + orderBy +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, params.Limit, params.Offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return crud.ListResult[Hero]{}, fmt.Errorf("list heroes: %w", err)
	}
	defer rows.Close()

	var items []Hero
	for rows.Next() {
		h, err := scanHero(rows)
		if err != nil {
			return crud.ListResult[Hero]{}, fmt.Errorf("scan hero: %w", err)
		}
		items = append(items, h)
	}
	if err := rows.Err(); err != nil {
		return crud.ListResult[Hero]{}, fmt.Errorf("list heroes: %w", err)
	}

	result := crud.ListResult[Hero]{Items: items}
	if params.WithTotal {
		countArgs := args[:len(args)-2] // drop limit/offset for the count query
		var total int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM heroes`+where, countArgs...).Scan(&total); err != nil {
			return crud.ListResult[Hero]{}, fmt.Errorf("count heroes: %w", err)
		}
		result.Total = &total
	}
	return result, nil
}

// comparisonOps maps crud's comparison filter ops to their SQL operator.
// "in" and "like" need their own clause shapes and are handled separately
// in buildWhere.
var comparisonOps = map[string]string{
	"eq": "=", "ne": "!=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=",
}

// buildWhere turns crud's allowlisted filters into a parameterized WHERE
// clause. Every op crud accepts is implemented explicitly; anything else —
// an op or a field this store doesn't know — is a 400, never silently
// treated as equality (which would return wrong results with no error) or
// dropped (which would return *more* rows than asked for).
func buildWhere(filters []crud.Filter) (string, []any, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}
	var clauses []string
	var args []any
	for _, f := range filters {
		column, ok := filterColumns[f.Field]
		if !ok {
			// crud already allowlisted this field, but never trust it twice.
			return "", nil, problem.BadRequest(fmt.Sprintf("field %q is not filterable", f.Field))
		}
		switch f.Op {
		case "in":
			args = append(args, strings.Split(f.Value, ","))
			clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", column, len(args)))
		case "like":
			args = append(args, escapeLike(f.Value))
			clauses = append(clauses, fmt.Sprintf(`%s ILIKE '%%' || $%d || '%%' ESCAPE '\'`, column, len(args)))
		default:
			sqlOp, ok := comparisonOps[f.Op]
			if !ok {
				return "", nil, problem.BadRequest(fmt.Sprintf("filter operator %q is not supported", f.Op))
			}
			args = append(args, f.Value)
			clauses = append(clauses, fmt.Sprintf("%s %s $%d", column, sqlOp, len(args)))
		}
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike makes a client-supplied "like" value match literally: without
// it, "%" or "_" in the value act as wildcards (`?name__like=%` would match
// every row).
func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}

func buildOrderBy(sortFields []crud.SortField) string {
	if len(sortFields) == 0 {
		return " ORDER BY name ASC"
	}
	var parts []string
	for _, sf := range sortFields {
		column, ok := filterColumns[sf.Field]
		if !ok {
			if sf.Field == "createdAt" {
				column = "created_at"
			} else {
				continue
			}
		}
		dir := "ASC"
		if sf.Desc {
			dir = "DESC"
		}
		parts = append(parts, column+" "+dir)
	}
	if len(parts) == 0 {
		return " ORDER BY name ASC"
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

// nullIfEmpty lets an optional, empty string column round-trip as SQL NULL
// instead of an empty string — nemesis_villain_id has no row to reference
// (the two services share no foreign key; see Hero.NemesisVillainID's doc
// comment), so "no nemesis set" should read back as NULL, not "".
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
