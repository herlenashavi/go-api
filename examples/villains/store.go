package main

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
)

// VillainStore is a map-behind-a-mutex store, same spirit as heroes's
// MemoryHeroStore — this example runs with zero external dependencies.
// It is not a crud.Store implementation: villains has no HTTP CRUD
// surface at all (see package doc), so there's no generic interface to
// satisfy here, just whatever methods VillainServer actually needs.
type VillainStore struct {
	mu       sync.RWMutex
	villains map[string]Villain
}

func NewVillainStore() *VillainStore {
	return &VillainStore{villains: make(map[string]Villain)}
}

func (s *VillainStore) List(statusFilter villainsv1.VillainStatus, limit, offset int) ([]Villain, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []Villain
	for _, v := range s.villains {
		if statusFilter != villainsv1.VillainStatus_VILLAIN_STATUS_UNSPECIFIED && v.Status != statusFilter {
			continue
		}
		matched = append(matched, v)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

	total := len(matched)
	if limit <= 0 {
		limit = 20
	}
	start := min(offset, len(matched))
	end := min(start+limit, len(matched))
	return matched[start:end], total
}

func (s *VillainStore) Get(id string) (Villain, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.villains[id]
	return v, ok
}

func (s *VillainStore) Create(name, evilPower, nemesisHeroID string, threatLevel villainsv1.ThreatLevel) Villain {
	now := time.Now().UTC()
	if threatLevel == villainsv1.ThreatLevel_THREAT_LEVEL_UNSPECIFIED {
		threatLevel = villainsv1.ThreatLevel_THREAT_LEVEL_MEDIUM
	}
	v := Villain{
		ID:            uuid.NewString(),
		Name:          name,
		EvilPower:     evilPower,
		ThreatLevel:   threatLevel,
		Status:        villainsv1.VillainStatus_VILLAIN_STATUS_ACTIVE,
		NemesisHeroID: nemesisHeroID,
		CreatedAt:     now,
		ModifiedAt:    now,
	}
	s.mu.Lock()
	s.villains[v.ID] = v
	s.mu.Unlock()
	return v
}

// Capture transitions a villain to CAPTURED. It is idempotent at the
// store layer — calling it again just re-sets the same status — the
// "can't capture twice" rule in this example lives in Cedar policy
// (policies/villains.cedar), not here, matching how heroes's "can't
// retire twice" rule is a policy concern rather than a store concern.
func (s *VillainStore) Capture(id string) (Villain, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.villains[id]
	if !ok {
		return Villain{}, false
	}
	v.Status = villainsv1.VillainStatus_VILLAIN_STATUS_CAPTURED
	v.ModifiedAt = time.Now().UTC()
	s.villains[id] = v
	return v, true
}
