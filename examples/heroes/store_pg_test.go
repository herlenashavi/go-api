package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// These exercise PGHeroStore's SQL building without a database: the
// query text and args are the whole contract between crud's parsed
// filters and Postgres.

func TestBuildWhere_EachOperator(t *testing.T) {
	cases := []struct {
		op       string
		value    string
		wantSQL  string
		wantArgs []any
	}{
		{"eq", "active", " WHERE status = $1", []any{"active"}},
		{"ne", "active", " WHERE status != $1", []any{"active"}},
		{"lt", "m", " WHERE status < $1", []any{"m"}},
		{"lte", "m", " WHERE status <= $1", []any{"m"}},
		{"gt", "m", " WHERE status > $1", []any{"m"}},
		{"gte", "m", " WHERE status >= $1", []any{"m"}},
		{"in", "active,retired", " WHERE status = ANY($1)", []any{[]string{"active", "retired"}}},
		{"like", "50%_off\\", ` WHERE status ILIKE '%' || $1 || '%' ESCAPE '\'`, []any{`50\%\_off\\`}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			sql, args, err := buildWhere([]crud.Filter{{Field: "status", Op: tc.op, Value: tc.value}})
			if err != nil {
				t.Fatalf("buildWhere: %v", err)
			}
			if sql != tc.wantSQL {
				t.Errorf("sql = %q, want %q", sql, tc.wantSQL)
			}
			if !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, tc.wantArgs)
			}
		})
	}
}

func TestBuildWhere_CombinesClausesWithSequentialPlaceholders(t *testing.T) {
	sql, args, err := buildWhere([]crud.Filter{
		{Field: "status", Op: "in", Value: "active,inactive"},
		{Field: "name", Op: "like", Value: "clark"},
	})
	if err != nil {
		t.Fatalf("buildWhere: %v", err)
	}
	want := ` WHERE status = ANY($1) AND name ILIKE '%' || $2 || '%' ESCAPE '\'`
	if sql != want {
		t.Fatalf("sql = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Fatalf("len(args) = %d, want 2", len(args))
	}
}

func TestBuildWhere_RejectsUnknownOpAndField(t *testing.T) {
	for name, f := range map[string]crud.Filter{
		"unknown op":    {Field: "status", Op: "between", Value: "a"},
		"unknown field": {Field: "super_power", Op: "eq", Value: "flight"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := buildWhere([]crud.Filter{f})
			if p := problem.From(err, true); err == nil || p.Status != 400 {
				t.Fatalf("err = %v, want a 400 problem", err)
			}
		})
	}
}

func TestBuildWhere_NoFilters(t *testing.T) {
	sql, args, err := buildWhere(nil)
	if err != nil || sql != "" || args != nil {
		t.Fatalf("buildWhere(nil) = %q, %v, %v; want empty", sql, args, err)
	}
}

// TestPGHeroStore_RejectsNonUUIDBeforeQuerying needs no database: a
// malformed id must be refused before the pool is touched (a nil pool
// here would panic if it were).
func TestPGHeroStore_RejectsNonUUIDBeforeQuerying(t *testing.T) {
	s := NewPGHeroStore(nil)
	ctx := context.Background()

	checks := map[string]error{}
	_, checks["get"] = s.Get(ctx, "not-a-uuid")
	_, checks["update"] = s.Update(ctx, "not-a-uuid", UpdateHero{})
	checks["delete"] = s.Delete(ctx, "not-a-uuid")

	for op, err := range checks {
		if p := problem.From(err, true); err == nil || p.Status != 400 {
			t.Errorf("%s: err = %v, want a 400 problem", op, err)
		}
	}
}
