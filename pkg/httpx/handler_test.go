package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/httpx"
)

type updateHeroInput struct {
	ID     uuid.UUID `path:"id"`
	Expand bool      `query:"expand"`
	Name   string    `json:"name" validate:"required"`
}

type updateHeroOutput struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func TestHandle_DecodesPathQueryAndBody(t *testing.T) {
	r := chi.NewRouter()
	r.Put("/heroes/{id}", httpx.Handle(func(ctx context.Context, in *updateHeroInput) (*updateHeroOutput, error) {
		if !in.Expand {
			t.Fatalf("expected expand=true to be bound from query string")
		}
		return &updateHeroOutput{ID: in.ID, Name: in.Name}, nil
	}, false))

	id := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/heroes/"+id.String()+"?expand=true",
		strings.NewReader(`{"name":"Clark"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), id.String()) {
		t.Fatalf("body missing path-bound id: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Clark") {
		t.Fatalf("body missing JSON-bound name: %s", rec.Body.String())
	}
}

func TestHandle_ValidationFailureReturns422Problem(t *testing.T) {
	r := chi.NewRouter()
	r.Put("/heroes/{id}", httpx.Handle(func(ctx context.Context, in *updateHeroInput) (*updateHeroOutput, error) {
		return &updateHeroOutput{}, nil
	}, false))

	req := httptest.NewRequest(http.MethodPut, "/heroes/"+uuid.New().String(), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"name"`) {
		t.Fatalf("expected field error naming \"name\": %s", rec.Body.String())
	}
}

func TestHandle_NoContentWritesEmpty204(t *testing.T) {
	r := chi.NewRouter()
	r.Delete("/heroes/{id}", httpx.Handle(func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*httpx.NoContent, error) {
		return &httpx.NoContent{}, nil
	}, false))

	req := httptest.NewRequest(http.MethodDelete, "/heroes/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %q", rec.Body.String())
	}
}
