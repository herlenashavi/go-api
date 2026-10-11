package main

import (
	"context"

	"github.com/google/uuid"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
)

type nemesisInput struct {
	ID uuid.UUID `path:"id"`
}

// villainInfo is a small, heroes-owned projection of the villains
// service's Villain message — heroes doesn't re-export villains' proto
// types in its own JSON API, it picks the fields its clients need. That
// decoupling is deliberate: villains' wire format can change shape
// without heroes' HTTP contract changing in lockstep.
type villainInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EvilPower   string `json:"evilPower"`
	ThreatLevel string `json:"threatLevel"`
	Status      string `json:"status"`
}

type nemesisOutput struct {
	HeroID   uuid.UUID    `json:"heroId"`
	HeroName string       `json:"heroName"`
	Villain  *villainInfo `json:"villain,omitempty"`
}

// nemesisHandler is heroes's one interservice call: given a hero, it asks
// the villains service (over gRPC, via VillainsClient) for the hero's
// nemesis. This is the pattern pkg/grpcx exists for — ctx here already
// carries the inbound request's ID and (because this route sits behind
// authn.Middleware) the caller's bearer token; VillainsClient's dial
// options forward both automatically, so villains's own authn/authz sees
// the same request ID in its logs and the same principal as heroes did,
// not a separate service identity.
//
// A hero with no NemesisVillainID set is not an error — it just has no
// Villain in the response. A villain ID that no longer resolves (the
// villains service doesn't have it, or is unreachable) *is* an error,
// surfaced via the grpcx.Problem mapping VillainsClient already applies.
func nemesisHandler(store heroStore, villains *VillainsClient) func(ctx context.Context, in *nemesisInput) (*nemesisOutput, error) {
	return func(ctx context.Context, in *nemesisInput) (*nemesisOutput, error) {
		hero, err := store.Get(ctx, in.ID.String())
		if err != nil {
			return nil, err
		}

		out := &nemesisOutput{HeroID: hero.ID, HeroName: hero.Name}
		if hero.NemesisVillainID == "" {
			return out, nil
		}

		villain, err := villains.GetVillain(ctx, hero.NemesisVillainID)
		if err != nil {
			return nil, err
		}

		out.Villain = &villainInfo{
			ID:          villain.Id,
			Name:        villain.Name,
			EvilPower:   villain.EvilPower,
			ThreatLevel: shortThreatLevel(villain.ThreatLevel),
			Status:      shortVillainStatus(villain.Status),
		}
		return out, nil
	}
}

// shortThreatLevel/shortVillainStatus render villains' proto enums in the
// lowercase style heroes' own HeroStatus already uses on the wire — each
// service owns its own JSON presentation of the other's data rather than
// importing a shared "how to render this enum" helper across the process
// boundary (there's nothing to import anyway: villains' equivalent
// mapping, shortStatus, is a private helper in its own main package).
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

func shortVillainStatus(s villainsv1.VillainStatus) string {
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
