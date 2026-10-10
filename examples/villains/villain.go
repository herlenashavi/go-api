// Package main is go-api's second worked example — a "villains" service
// that exposes no HTTP business API at all, only gRPC (plus the usual
// HTTP health/metrics endpoints every go-api service gets for free). It
// exists to demonstrate pkg/grpcx: a server with the same
// observability/auth posture as the HTTP side, called by examples/heroes
// as a client (see that service's villains_client.go and nemesis.go).
package main

import (
	"time"

	villainsv1 "github.com/herlenashavi/go-api/gen/villains/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Villain is the service's one domain type. Unlike examples/heroes, there
// are no separate Create/Update Go structs here — the generated
// villainsv1.CreateVillainRequest/CaptureVillainRequest messages already
// are the "input shape," since the gRPC contract (proto/villains/v1) is
// the thing defining what's accepted, the same role CreateHero/UpdateHero
// play for the HTTP side.
type Villain struct {
	ID            string
	Name          string
	EvilPower     string
	ThreatLevel   villainsv1.ThreatLevel
	Status        villainsv1.VillainStatus
	NemesisHeroID string
	CreatedAt     time.Time
	ModifiedAt    time.Time
}

// shortStatus renders a VillainStatus the way policies/villains.cedar
// compares it — lowercase, without the VILLAIN_STATUS_ prefix the raw
// enum's String() method would include — matching the plain lowercase
// status strings examples/heroes already uses for HeroStatus.
func shortStatus(s villainsv1.VillainStatus) string {
	switch s {
	case villainsv1.VillainStatus_VILLAIN_STATUS_ACTIVE:
		return "active"
	case villainsv1.VillainStatus_VILLAIN_STATUS_CAPTURED:
		return "captured"
	case villainsv1.VillainStatus_VILLAIN_STATUS_DEFEATED:
		return "defeated"
	default:
		return "unspecified"
	}
}

// toProto converts to the wire type, filling in protobuf's well-known
// Timestamp for the two time.Time fields.
func (v Villain) toProto() *villainsv1.Villain {
	return &villainsv1.Villain{
		Id:            v.ID,
		Name:          v.Name,
		EvilPower:     v.EvilPower,
		ThreatLevel:   v.ThreatLevel,
		Status:        v.Status,
		NemesisHeroId: v.NemesisHeroID,
		CreatedAt:     timestamppb.New(v.CreatedAt),
		ModifiedAt:    timestamppb.New(v.ModifiedAt),
	}
}
