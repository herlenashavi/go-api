package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// MemoryHeroStore is a crud.Store[Hero, CreateHero, UpdateHero] backed by a
// map behind a mutex. It exists so this example runs with zero external
// dependencies; a real service swaps this for a dbx/pgx-backed store (see
// store_pg.go) without changing anything in main.go except which one gets
// constructed — that's the point of depending on crud.Store, not a
// concrete type.
type MemoryHeroStore struct {
	mu     sync.RWMutex
	heroes map[uuid.UUID]Hero
}

func NewMemoryHeroStore() *MemoryHeroStore {
	return &MemoryHeroStore{heroes: make(map[uuid.UUID]Hero)}
}

func (s *MemoryHeroStore) List(_ context.Context, params crud.ListParams) (crud.ListResult[Hero], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Hero, 0, len(s.heroes))
	for _, h := range s.heroes {
		ok, err := matchesAll(h, params.Filters)
		if err != nil {
			return crud.ListResult[Hero]{}, err
		}
		if ok {
			matched = append(matched, h)
		}
	}

	sortHeroes(matched, params.Sort)

	total := len(matched)
	result := crud.ListResult[Hero]{}
	if params.WithTotal {
		result.Total = &total
	}

	start := min(params.Offset, len(matched))
	end := min(start+params.Limit, len(matched))
	result.Items = matched[start:end]
	return result, nil
}

func matchesAll(h Hero, filters []crud.Filter) (bool, error) {
	for _, f := range filters {
		ok, err := matchesOne(h, f)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// matchesOne mirrors PGHeroStore's buildWhere: every op crud accepts is
// implemented, and an unknown field or op is a 400 — never silently
// "matches everything", which would return rows the client filtered out.
func matchesOne(h Hero, f crud.Filter) (bool, error) {
	var actual string
	switch f.Field {
	case "status":
		actual = string(h.Status)
	case "name":
		actual = h.Name
	default:
		return false, problem.BadRequest(fmt.Sprintf("field %q is not filterable", f.Field))
	}

	switch f.Op {
	case "eq":
		return actual == f.Value, nil
	case "ne":
		return actual != f.Value, nil
	case "lt":
		return actual < f.Value, nil
	case "lte":
		return actual <= f.Value, nil
	case "gt":
		return actual > f.Value, nil
	case "gte":
		return actual >= f.Value, nil
	case "in":
		return slices.Contains(strings.Split(f.Value, ","), actual), nil
	case "like":
		return strings.Contains(strings.ToLower(actual), strings.ToLower(f.Value)), nil
	default:
		return false, problem.BadRequest(fmt.Sprintf("filter operator %q is not supported", f.Op))
	}
}

func sortHeroes(heroes []Hero, fields []crud.SortField) {
	if len(fields) == 0 {
		fields = []crud.SortField{{Field: "name"}}
	}
	sort.SliceStable(heroes, func(i, j int) bool {
		for _, f := range fields {
			var less, greater bool
			switch f.Field {
			case "name":
				less, greater = heroes[i].Name < heroes[j].Name, heroes[i].Name > heroes[j].Name
			case "createdAt":
				less = heroes[i].CreatedAt.Before(heroes[j].CreatedAt)
				greater = heroes[i].CreatedAt.After(heroes[j].CreatedAt)
			}
			if f.Desc {
				less, greater = greater, less
			}
			if less {
				return true
			}
			if greater {
				return false
			}
		}
		return false
	})
}

func (s *MemoryHeroStore) Get(_ context.Context, id string) (Hero, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Hero{}, problem.BadRequest("id is not a valid UUID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.heroes[parsed]
	if !ok {
		return Hero{}, problem.NotFound("Hero")
	}
	return h, nil
}

func (s *MemoryHeroStore) Create(_ context.Context, in CreateHero) (Hero, error) {
	now := time.Now().UTC()
	status := in.Status
	if status == "" {
		status = HeroStatusActive
	}
	h := Hero{
		ID:               uuid.New(),
		Name:             in.Name,
		SuperPower:       in.SuperPower,
		Status:           status,
		NemesisVillainID: in.NemesisVillainID,
		CreatedAt:        now,
		ModifiedAt:       now,
	}
	s.mu.Lock()
	s.heroes[h.ID] = h
	s.mu.Unlock()
	return h, nil
}

func (s *MemoryHeroStore) Update(_ context.Context, id string, in UpdateHero) (Hero, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Hero{}, problem.BadRequest("id is not a valid UUID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.heroes[parsed]
	if !ok {
		return Hero{}, problem.NotFound("Hero")
	}

	if in.Name != nil {
		h.Name = *in.Name
	}
	if in.SuperPower != nil {
		h.SuperPower = *in.SuperPower
	}
	if in.Status != nil {
		h.Status = *in.Status
	}
	if in.NemesisVillainID != nil {
		h.NemesisVillainID = *in.NemesisVillainID
	}
	h.ModifiedAt = time.Now().UTC()

	s.heroes[parsed] = h
	return h, nil
}

func (s *MemoryHeroStore) Delete(_ context.Context, id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return problem.BadRequest("id is not a valid UUID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.heroes[parsed]; !ok {
		return problem.NotFound("Hero")
	}
	delete(s.heroes, parsed)
	return nil
}

// Retire is a hand-written, non-CRUD operation on the same store — the
// kind of thing that doesn't fit Store's five methods and is exactly why
// crud.Resource is opt-in rather than the only way to expose a resource.
// See retire.go for the route built on top of this.
func (s *MemoryHeroStore) Retire(_ context.Context, id uuid.UUID) (Hero, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.heroes[id]
	if !ok {
		return Hero{}, problem.NotFound("Hero")
	}
	h.Status = HeroStatusRetired
	h.ModifiedAt = time.Now().UTC()
	s.heroes[id] = h
	return h, nil
}
