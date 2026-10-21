package main

import (
	"context"
	"testing"

	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/problem"
)

func TestMemoryHeroStore_ListFilters(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryHeroStore()
	for _, in := range []CreateHero{
		{Name: "Bruce", SuperPower: "money", Status: HeroStatusActive},
		{Name: "Clark", SuperPower: "flight", Status: HeroStatusRetired},
		{Name: "Diana", SuperPower: "lasso", Status: HeroStatusInactive},
	} {
		if _, err := s.Create(ctx, in); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	cases := []struct {
		filter crud.Filter
		want   []string
	}{
		{crud.Filter{Field: "status", Op: "in", Value: "active,retired"}, []string{"Bruce", "Clark"}},
		{crud.Filter{Field: "name", Op: "gt", Value: "Bruce"}, []string{"Clark", "Diana"}},
		{crud.Filter{Field: "name", Op: "lte", Value: "Clark"}, []string{"Bruce", "Clark"}},
		{crud.Filter{Field: "name", Op: "like", Value: "ark"}, []string{"Clark"}},
		{crud.Filter{Field: "status", Op: "ne", Value: "active"}, []string{"Clark", "Diana"}},
	}
	for _, tc := range cases {
		res, err := s.List(ctx, crud.ListParams{Limit: 10, Filters: []crud.Filter{tc.filter}})
		if err != nil {
			t.Fatalf("%+v: %v", tc.filter, err)
		}
		var got []string
		for _, h := range res.Items {
			got = append(got, h.Name)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%+v: got %v, want %v", tc.filter, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%+v: got %v, want %v", tc.filter, got, tc.want)
			}
		}
	}
}

func TestMemoryHeroStore_ListRejectsUnknownOp(t *testing.T) {
	s := NewMemoryHeroStore()
	if _, err := s.Create(context.Background(), CreateHero{Name: "Bruce", SuperPower: "money"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.List(context.Background(), crud.ListParams{Limit: 10, Filters: []crud.Filter{{Field: "status", Op: "between", Value: "a"}}})
	if p := problem.From(err, true); err == nil || p.Status != 400 {
		t.Fatalf("err = %v, want a 400 problem", err)
	}
}
