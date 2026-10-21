package crud

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/httpx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// Actions names the Cedar action for each operation. Zero-value fields fall
// back to "<lowercase ResourceType>:<op>" (e.g. "hero:list"), so you only
// need to set these if your policies use different names.
type Actions struct {
	List, Get, Create, Update, Delete authz.Action
}

// Config configures a Resource.
type Config[T, CreateIn, UpdateIn any] struct {
	// ResourceType is the Cedar entity type for this resource (e.g. "Hero")
	// and is also used to derive default action names.
	ResourceType string
	Store        Store[T, CreateIn, UpdateIn]

	// Authorizer enforces every route if set. Leaving it nil mounts the
	// resource with no authorization checks at all — only do that for a
	// genuinely public, read-only resource, and say so in a comment where
	// you do it.
	Authorizer *authz.Authorizer
	Actions    Actions

	// SortableFields/FilterableFields allowlist what ?sort= and
	// ?field[__op]= may reference; both default to empty (nothing
	// sortable or filterable) if unset. See pkg/crud's package doc.
	SortableFields   map[string]bool
	FilterableFields map[string]bool

	DefaultPageSize int // default 20
	MaxPageSize     int // default 100
}

func (c *Config[T, CreateIn, UpdateIn]) withDefaults() {
	lower := strings.ToLower(c.ResourceType)
	if c.Actions.List == "" {
		c.Actions.List = authz.Action(lower + ":list")
	}
	if c.Actions.Get == "" {
		c.Actions.Get = authz.Action(lower + ":read")
	}
	if c.Actions.Create == "" {
		c.Actions.Create = authz.Action(lower + ":create")
	}
	if c.Actions.Update == "" {
		c.Actions.Update = authz.Action(lower + ":update")
	}
	if c.Actions.Delete == "" {
		c.Actions.Delete = authz.Action(lower + ":delete")
	}
	if c.DefaultPageSize <= 0 {
		c.DefaultPageSize = 20
	}
	if c.MaxPageSize <= 0 {
		c.MaxPageSize = 100
	}
	if c.SortableFields == nil {
		c.SortableFields = map[string]bool{}
	}
	if c.FilterableFields == nil {
		c.FilterableFields = map[string]bool{}
	}
}

// idInput is the path-only input for Get/Delete, whose bodies carry nothing
// Store-specific — unlike Create/Update, there's no generic type parameter
// to combine with a path field here (Go doesn't allow embedding a type
// parameter), so this one's just a concrete struct.
type idInput struct {
	ID string `path:"id"`
}

// NewResource builds a chi.Router exposing the standard CRUD routes for T,
// backed by store. Debug controls whether unexpected errors leak detail to
// clients (pass your service's config.Base.Debug) — mirrored into every
// httpx.Handle call this resource makes.
//
//	r.Mount("/heroes", crud.NewResource(crud.Config[Hero, CreateHero, UpdateHero]{
//		ResourceType: "Hero",
//		Store:        heroStore,
//		Authorizer:   authorizer,
//		FilterableFields: map[string]bool{"status": true, "teamId": true},
//		SortableFields:   map[string]bool{"name": true, "createdAt": true},
//	}, debug))
func NewResource[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn], debug bool) chi.Router {
	cfg.withDefaults()
	r := chi.NewRouter()

	r.Get("/", listHandler(cfg, debug))
	r.Post("/", httpx.Handle(createHandler(cfg), debug, httpx.WithStatus(http.StatusCreated)))
	r.Get("/{id}", httpx.Handle(getHandler(cfg), debug))
	r.Put("/{id}", updateHandler(cfg, debug))
	r.Patch("/{id}", updateHandler(cfg, debug))
	r.Delete("/{id}", httpx.Handle(deleteHandler(cfg), debug))

	return r
}

func authorize[T, CreateIn, UpdateIn any](ctx context.Context, cfg Config[T, CreateIn, UpdateIn], action authz.Action, resourceID string) error {
	if cfg.Authorizer == nil {
		return nil
	}
	principal, ok := authn.FromContext(ctx)
	if !ok || principal == nil {
		return problem.Unauthorized("")
	}
	ref := authz.CollectionResource(cfg.ResourceType)
	if resourceID != "" {
		ref = authz.ResourceRef{Type: cfg.ResourceType, ID: resourceID}
	}
	return cfg.Authorizer.Authorize(ctx, principal, action, ref, nil)
}

func listHandler[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn], debug bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := authorize(r.Context(), cfg, cfg.Actions.List, ""); err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		params, err := parseListParams(r, listParamsConfig{
			DefaultPageSize:  cfg.DefaultPageSize,
			MaxPageSize:      cfg.MaxPageSize,
			SortableFields:   cfg.SortableFields,
			FilterableFields: cfg.FilterableFields,
		})
		if err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		result, err := cfg.Store.List(r.Context(), params)
		if err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		meta := httpx.Meta{NextCursor: result.NextCursor, Total: result.Total}
		if result.NextCursor == "" {
			offset, limit := params.Offset, params.Limit
			meta.Offset, meta.Limit = &offset, &limit
		}
		httpx.Respond(w, r, http.StatusOK, httpx.Page[T]{Data: result.Items, Meta: meta})
	}
}

func getHandler[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn]) func(context.Context, *idInput) (*T, error) {
	return func(ctx context.Context, in *idInput) (*T, error) {
		if err := authorize(ctx, cfg, cfg.Actions.Get, in.ID); err != nil {
			return nil, err
		}
		item, err := cfg.Store.Get(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &item, nil
	}
}

func createHandler[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn]) func(context.Context, *CreateIn) (*T, error) {
	return func(ctx context.Context, in *CreateIn) (*T, error) {
		if err := authorize(ctx, cfg, cfg.Actions.Create, ""); err != nil {
			return nil, err
		}
		item, err := cfg.Store.Create(ctx, *in)
		if err != nil {
			return nil, err
		}
		return &item, nil
	}
}

func deleteHandler[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn]) func(context.Context, *idInput) (*httpx.NoContent, error) {
	return func(ctx context.Context, in *idInput) (*httpx.NoContent, error) {
		if err := authorize(ctx, cfg, cfg.Actions.Delete, in.ID); err != nil {
			return nil, err
		}
		if err := cfg.Store.Delete(ctx, in.ID); err != nil {
			return nil, err
		}
		return &httpx.NoContent{}, nil
	}
}

// updateHandler is written by hand rather than via httpx.Handle: the route
// needs both a path ID and a generically-typed body in one request, and Go
// does not allow embedding a type parameter to combine them into a single
// struct httpx.Decode could bind (see idInput's doc comment). It still
// reuses httpx.Validate/httpx.Respond/problem for everything else, so the
// wire behavior (validation errors, problem+json, status codes) is
// identical to a route that *could* use Handle.
func updateHandler[T, CreateIn, UpdateIn any](cfg Config[T, CreateIn, UpdateIn], debug bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := authorize(r.Context(), cfg, cfg.Actions.Update, id); err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		var in UpdateIn
		if err := httpx.DecodeJSONBody(r, &in); err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}
		if err := httpx.Validate(&in); err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		item, err := cfg.Store.Update(r.Context(), id, in)
		if err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}
		httpx.Respond(w, r, http.StatusOK, item)
	}
}
