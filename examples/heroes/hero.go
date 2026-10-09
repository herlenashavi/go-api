// Package main is go-api's worked example — a small "heroes" service that
// exercises every piece of the framework: config, structured logging,
// tracing/metrics, JWT-shaped authn, Cedar-based RBAC authz, generic CRUD,
// a hand-written non-CRUD route, and health checks. It runs with no
// external dependencies (`go run .`) using an in-memory store; swapping
// heroStore for a dbx/pgx-backed one is a few lines, shown in store_pg.go.
package main

import (
	"time"

	"github.com/google/uuid"
)

// HeroStatus mirrors canvass-fastapi's test-dummy-project example, which
// this service is a spiritual successor to.
type HeroStatus string

const (
	HeroStatusActive   HeroStatus = "active"
	HeroStatusInactive HeroStatus = "inactive"
	HeroStatusRetired  HeroStatus = "retired"
)

// Hero is the resource's response shape — what crud.Resource serializes
// for GET/POST/PUT/PATCH. Field names are camelCase on the wire via the
// `json` tags, per go-api's wire-format convention.
type Hero struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	SuperPower string     `json:"superPower"`
	Status     HeroStatus `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	ModifiedAt time.Time  `json:"modifiedAt"`
}

// CreateHero is the POST body. No id/timestamps — those are server-assigned,
// the same exclusion canvass-fastapi's schema_factory achieved by stripping
// NON_EDITABLE_FIELDS, but here it's just... a smaller struct.
type CreateHero struct {
	Name       string     `json:"name" validate:"required"`
	SuperPower string     `json:"superPower" validate:"required"`
	Status     HeroStatus `json:"status" validate:"omitempty,oneof=active inactive retired"`
}

// UpdateHero is the PUT/PATCH body. Every field is a pointer so a PATCH can
// send just the fields it's changing; HeroStore.Update only applies the
// ones that are non-nil — the same partial-update semantics
// canvass-fastapi got from `model_dump(exclude_unset=True)`, but visible
// directly in the type instead of inferred from what Pydantic decided was
// "unset".
type UpdateHero struct {
	Name       *string     `json:"name"`
	SuperPower *string     `json:"superPower"`
	Status     *HeroStatus `json:"status" validate:"omitempty,oneof=active inactive retired"`
}
