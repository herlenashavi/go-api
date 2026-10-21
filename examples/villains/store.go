package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// VillainStore is a map-behind-a-mutex store, same spirit as heroes's
// MemoryHeroStore — this example runs with zero external dependencies.
// It is not a crud.Store implementation: villains has no HTTP CRUD
// surface at all (see package doc), so there's no generic interface to
// satisfy here, just villainStore (server.go) — whatever methods
// VillainServer actually needs, which VillainPGStore (store_pg.go)
// implements too.
type VillainStore struct {
	mu       sync.RWMutex
	villains map[string]Villain
}

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// normalizePage bounds ListVillainsRequest's client-supplied paging the
// same way crud.Resource bounds its HTTP list endpoints (default 20, max
// 100), for both stores: limit and offset arrive straight off the wire as
// int32s, so without this a caller could ask for 2^31 rows in one RPC, and
// a negative offset would slice out of range in VillainStore.
func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	return min(limit, maxPageSize), max(offset, 0)
}

func NewVillainStore() *VillainStore {
	return &VillainStore{villains: make(map[string]Villain)}
}

// ctx is unused here — an in-memory map can't fail or block — but it's
// part of villainStore because VillainPGStore's queries need one.
func (s *VillainStore) List(_ context.Context, statusFilter villainsv1.VillainStatus, limit, offset int) ([]Villain, int, error) {
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
	limit, offset = normalizePage(limit, offset)
	start := min(offset, len(matched))
	end := min(start+limit, len(matched))
	return matched[start:end], total, nil
}

func (s *VillainStore) Get(_ context.Context, id string) (Villain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.villains[id]
	if !ok {
		return Villain{}, problem.NotFound("Villain")
	}
	return v, nil
}

func (s *VillainStore) Create(_ context.Context, name, evilPower, nemesisHeroID string, threatLevel villainsv1.ThreatLevel) (Villain, error) {
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
	return v, nil
}

// Capture transitions a villain to CAPTURED. It is idempotent at the
// store layer — calling it again just re-sets the same status — the
// "can't capture twice" rule in this example lives in Cedar policy
// (policies/villains.cedar), not here, matching how heroes's "can't
// retire twice" rule is a policy concern rather than a store concern.
func (s *VillainStore) Capture(_ context.Context, id string) (Villain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.villains[id]
	if !ok {
		return Villain{}, problem.NotFound("Villain")
	}
	v.Status = villainsv1.VillainStatus_VILLAIN_STATUS_CAPTURED
	v.ModifiedAt = time.Now().UTC()
	s.villains[id] = v
	return v, nil
}
