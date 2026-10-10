package crud

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/herlenashavi/go-api/pkg/problem"
)

var validOps = map[string]bool{
	"eq": true, "ne": true, "lt": true, "lte": true,
	"gt": true, "gte": true, "like": true, "in": true,
}

// reservedListParams are query keys parseListParams itself consumes; they
// can never collide with a filter field name because they're checked
// before the allowlist below is even consulted.
var reservedListParams = map[string]bool{
	"limit": true, "offset": true, "cursor": true, "sort": true, "count": true,
}

type listParamsConfig struct {
	DefaultPageSize  int
	MaxPageSize      int
	SortableFields   map[string]bool
	FilterableFields map[string]bool
}

func parseListParams(r *http.Request, cfg listParamsConfig) (ListParams, error) {
	q := r.URL.Query()
	params := ListParams{Limit: cfg.DefaultPageSize, Cursor: q.Get("cursor")}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return params, problem.BadRequest("limit must be a positive integer")
		}
		if n > cfg.MaxPageSize {
			return params, problem.BadRequest(fmt.Sprintf("limit must not exceed %d", cfg.MaxPageSize))
		}
		params.Limit = n
	}

	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return params, problem.BadRequest("offset must be a non-negative integer")
		}
		params.Offset = n
	}

	if v := q.Get("count"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return params, problem.BadRequest("count must be a boolean")
		}
		params.WithTotal = b
	}

	for _, raw := range q["sort"] {
		for _, field := range strings.Split(raw, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			desc := strings.HasPrefix(field, "-")
			name := strings.TrimPrefix(field, "-")
			if !cfg.SortableFields[name] {
				return params, problem.BadRequest(fmt.Sprintf("field %q is not sortable", name))
			}
			params.Sort = append(params.Sort, SortField{Field: name, Desc: desc})
		}
	}

	for key, values := range q {
		if reservedListParams[key] || key == "sort" || len(values) == 0 || values[0] == "" {
			continue
		}
		field, op, found := strings.Cut(key, "__")
		if !found || !validOps[op] {
			field, op = key, "eq"
		}
		if !cfg.FilterableFields[field] {
			return params, problem.BadRequest(fmt.Sprintf("field %q is not filterable", field))
		}
		params.Filters = append(params.Filters, Filter{Field: field, Op: op, Value: values[0]})
	}

	return params, nil
}
