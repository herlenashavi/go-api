package main

import (
	"context"
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
		if matchesAll(h, params.Filters) {
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

func matchesAll(h Hero, filters []crud.Filter) bool {
	for _, f := range filters {
		if !matchesOne(h, f) {
			return false
		}
	}
	return true
}

func matchesOne(h Hero, f crud.Filter) bool {
	switch f.Field {
	case "status":
		switch f.Op {
		case "eq":
			return string(h.Status) == f.Value
		case "ne":
			return string(h.Status) != f.Value
		case "in":
			for _, v := range strings.Split(f.Value, ",") {
				if string(h.Status) == v {
					return true
				}
			}
			return false
		}
	case "name":
		if f.Op == "like" {
			return strings.Contains(strings.ToLower(h.Name), strings.ToLower(f.Value))
		}
	}
	return true // an allowlisted-but-unhandled (field, op) pair matches everything rather than erroring
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
		ID:         uuid.New(),
		Name:       in.Name,
		SuperPower: in.SuperPower,
		Status:     status,
		CreatedAt:  now,
		ModifiedAt: now,
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
