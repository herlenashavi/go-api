// Package crud is go-api's opt-in generic CRUD layer: given a Store
// implementation, Resource mounts list/get/create/update/delete routes with
// authz enforcement, allowlisted filtering/sorting, and consistent
// pagination — the part of canvass-fastapi's ModelRouter worth keeping,
// rebuilt on Go generics instead of a runtime schema_factory().
//
// It is entirely optional: a service that doesn't fit the CRUD shape just
// writes chi routes and httpx.Handle calls directly (see pkg/httpx) and
// never imports this package.
package crud

import "context"

// ListParams carries the parsed, *allowlisted* query parameters for a list
// request. Resource builds this from the request using the
// SortableFields/FilterableFields allowlists in Config — a Store
// implementation never sees a field name that wasn't explicitly permitted,
// unlike canvass-fastapi's filter_factory, which made every model column
// filterable via raw getattr().
type ListParams struct {
	Limit   int
	Offset  int
	Cursor  string
	Sort    []SortField
	Filters []Filter
	// WithTotal is true when the client asked for a total count (?count=true),
	// since counting is an extra query a Store should only pay for when needed.
	WithTotal bool
}

// SortField is one `sort=field` or `sort=-field` query parameter.
type SortField struct {
	Field string
	Desc  bool
}

// Filter is one allowlisted `field` or `field__op` query parameter.
// Supported Op values: eq, ne, lt, lte, gt, gte, like, in. "in" expects
// Value to be comma-separated.
type Filter struct {
	Field string
	Op    string
	Value string
}

// ListResult is what Store.List returns: the page of items plus whatever
// pagination metadata it was able to compute.
type ListResult[T any] struct {
	Items []T
	// NextCursor is the opaque token for the next page, "" if there is
	// none. Leave unset if your Store only supports offset pagination.
	NextCursor string
	// Total is populated only when ListParams.WithTotal was set.
	Total *int
}

// Store is what Resource needs from your persistence layer — typically a
// thin adapter over sqlc-generated queries against a dbx.Pool. T is the
// resource's response shape; CreateIn/UpdateIn are the accepted request
// bodies for POST and PUT/PATCH respectively (commonly narrower than T —
// no server-generated id/createdAt/etc — and commonly all-pointer fields
// on UpdateIn to support partial updates).
//
// Resource calls these only after authorization has already passed; a
// Store never needs to re-check permissions for the request as a whole,
// though row-level ABAC that depends on the loaded record's own attributes
// (not just its id) is still the Store/handler's job — see
// authz.Authorizer.Check from inside Get/Update if you need that.
type Store[T, CreateIn, UpdateIn any] interface {
	List(ctx context.Context, params ListParams) (ListResult[T], error)
	Get(ctx context.Context, id string) (T, error)
	Create(ctx context.Context, in CreateIn) (T, error)
	Update(ctx context.Context, id string, in UpdateIn) (T, error)
	Delete(ctx context.Context, id string) error
}
