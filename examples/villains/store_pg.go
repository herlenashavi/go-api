// store_pg.go is villains's version of examples/heroes/store_pg.go: a
// dbx/pgx-backed villainStore (server.go), used automatically by main.go
// whenever DATABASE_URL is set instead of VillainStore (store.go). The
// schema it queries comes from migrations/, applied separately by the
// migrate/ binary — not by this process at boot.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/dbx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

type VillainPGStore struct {
	pool *dbx.Pool
}

func NewVillainPGStore(pool *dbx.Pool) *VillainPGStore {
	return &VillainPGStore{pool: pool}
}

// Compile-time proof this satisfies the same interface VillainStore
// does — see heroes/store_pg.go's identical assertion for why that
// matters.
var _ villainStore = (*VillainPGStore)(nil)

const villainColumns = "id, name, evil_power, threat_level, status, nemesis_hero_id, created_at, modified_at"

func scanVillain(row pgx.Row) (Villain, error) {
	var v Villain
	var threatLevel, status string
	var nemesisHeroID *string
	if err := row.Scan(&v.ID, &v.Name, &v.EvilPower, &threatLevel, &status, &nemesisHeroID, &v.CreatedAt, &v.ModifiedAt); err != nil {
		return Villain{}, err
	}
	v.ThreatLevel = parseThreatLevel(threatLevel)
	v.Status = parseVillainStatus(status)
	if nemesisHeroID != nil {
		v.NemesisHeroID = *nemesisHeroID
	}
	return v, nil
}

func (s *VillainPGStore) Get(ctx context.Context, id string) (Villain, error) {
	v, err := scanVillain(s.pool.QueryRow(ctx, `SELECT `+villainColumns+` FROM villains WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Villain{}, problem.NotFound("Villain")
		}
		return Villain{}, fmt.Errorf("get villain: %w", err)
	}
	return v, nil
}

func (s *VillainPGStore) Create(ctx context.Context, name, evilPower, nemesisHeroID string, threatLevel villainsv1.ThreatLevel) (Villain, error) {
	if threatLevel == villainsv1.ThreatLevel_THREAT_LEVEL_UNSPECIFIED {
		threatLevel = villainsv1.ThreatLevel_THREAT_LEVEL_MEDIUM
	}
	v, err := scanVillain(s.pool.QueryRow(ctx,
		`INSERT INTO villains (name, evil_power, threat_level, nemesis_hero_id)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+villainColumns,
		name, evilPower, shortThreatLevel(threatLevel), nullIfEmpty(nemesisHeroID),
	))
	if err != nil {
		return Villain{}, fmt.Errorf("create villain: %w", err)
	}
	return v, nil
}

// Capture is idempotent, same as VillainStore.Capture — see that
// method's doc comment for why "can't capture twice" is a Cedar policy
// concern, not a store one.
func (s *VillainPGStore) Capture(ctx context.Context, id string) (Villain, error) {
	v, err := scanVillain(s.pool.QueryRow(ctx,
		`UPDATE villains SET status = 'captured', modified_at = now() WHERE id = $1 RETURNING `+villainColumns,
		id,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Villain{}, problem.NotFound("Villain")
		}
		return Villain{}, fmt.Errorf("capture villain: %w", err)
	}
	return v, nil
}

func (s *VillainPGStore) List(ctx context.Context, statusFilter villainsv1.VillainStatus, limit, offset int) ([]Villain, int, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	var args []any
	if statusFilter != villainsv1.VillainStatus_VILLAIN_STATUS_UNSPECIFIED {
		where = " WHERE status = $1"
		args = append(args, shortStatus(statusFilter))
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM villains`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count villains: %w", err)
	}

	query := `SELECT ` + villainColumns + ` FROM villains` + where +
		fmt.Sprintf(" ORDER BY name ASC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list villains: %w", err)
	}
	defer rows.Close()

	var items []Villain
	for rows.Next() {
		v, err := scanVillain(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan villain: %w", err)
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list villains: %w", err)
	}
	return items, total, nil
}

// nullIfEmpty lets an optional, empty string column round-trip as SQL
// NULL instead of an empty string — same reasoning as heroes'
// nullIfEmpty (store_pg.go there): nemesis_hero_id has no row to
// reference in this database, so "no nemesis set" should read back as
// NULL, not "".
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseThreatLevel/parseVillainStatus are shortThreatLevel/shortStatus's
// inverse: turning the lowercase text this package's own CHECK
// constraints store back into the proto enum Villain.toProto() expects.
// (heroes/nemesis.go has its own shortThreatLevel/shortVillainStatus —
// see that file's doc comment on why each service renders the other's
// enums itself rather than sharing a helper across the process boundary;
// the same reasoning is why villains doesn't import those instead of
// defining shortThreatLevel below.)
func parseThreatLevel(s string) villainsv1.ThreatLevel {
	switch s {
	case "low":
		return villainsv1.ThreatLevel_THREAT_LEVEL_LOW
	case "medium":
		return villainsv1.ThreatLevel_THREAT_LEVEL_MEDIUM
	case "high":
		return villainsv1.ThreatLevel_THREAT_LEVEL_HIGH
	case "critical":
		return villainsv1.ThreatLevel_THREAT_LEVEL_CRITICAL
	default:
		return villainsv1.ThreatLevel_THREAT_LEVEL_UNSPECIFIED
	}
}

func parseVillainStatus(s string) villainsv1.VillainStatus {
	switch s {
	case "active":
		return villainsv1.VillainStatus_VILLAIN_STATUS_ACTIVE
	case "captured":
		return villainsv1.VillainStatus_VILLAIN_STATUS_CAPTURED
	case "defeated":
		return villainsv1.VillainStatus_VILLAIN_STATUS_DEFEATED
	default:
		return villainsv1.VillainStatus_VILLAIN_STATUS_UNSPECIFIED
	}
}

// shortThreatLevel renders a ThreatLevel the way this package's own
// migrations' CHECK (threat_level IN (...)) constraint expects —
// lowercase, no THREAT_LEVEL_ prefix — matching shortStatus's existing
// treatment of VillainStatus in villain.go. Create's UNSPECIFIED->MEDIUM
// defaulting above means the "unspecified" branch is never actually
// written; it exists so this function is total, not partial.
func shortThreatLevel(t villainsv1.ThreatLevel) string {
	switch t {
	case villainsv1.ThreatLevel_THREAT_LEVEL_LOW:
		return "low"
	case villainsv1.ThreatLevel_THREAT_LEVEL_MEDIUM:
		return "medium"
	case villainsv1.ThreatLevel_THREAT_LEVEL_HIGH:
		return "high"
	case villainsv1.ThreatLevel_THREAT_LEVEL_CRITICAL:
		return "critical"
	default:
		return "unspecified"
	}
}
