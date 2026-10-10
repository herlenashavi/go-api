package crud_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/crud"
)

type widget struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type createWidget struct {
	Name   string `json:"name" validate:"required"`
	Status string `json:"status"`
}

type updateWidget struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

type memStore struct {
	mu      sync.Mutex
	items   map[string]widget
	nextSeq int
}

func newMemStore() *memStore { return &memStore{items: map[string]widget{}} }

func (s *memStore) List(_ context.Context, params crud.ListParams) (crud.ListResult[widget], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []widget
	for _, w := range s.items {
		if matchesFilters(w, params.Filters) {
			items = append(items, w)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	for _, sf := range params.Sort {
		if sf.Field == "name" && sf.Desc {
			sort.Slice(items, func(i, j int) bool { return items[i].Name > items[j].Name })
		}
	}

	total := len(items)
	start := params.Offset
	if start > len(items) {
		start = len(items)
	}
	end := start + params.Limit
	if end > len(items) {
		end = len(items)
	}

	result := crud.ListResult[widget]{Items: items[start:end]}
	if params.WithTotal {
		result.Total = &total
	}
	return result, nil
}

func matchesFilters(w widget, filters []crud.Filter) bool {
	for _, f := range filters {
		if f.Field == "status" && f.Op == "eq" && w.Status != f.Value {
			return false
		}
	}
	return true
}

func (s *memStore) Get(_ context.Context, id string) (widget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.items[id]
	if !ok {
		return widget{}, notFoundErr{id}
	}
	return w, nil
}

type notFoundErr struct{ id string }

func (e notFoundErr) Error() string { return fmt.Sprintf("widget %q not found", e.id) }

func (s *memStore) Create(_ context.Context, in createWidget) (widget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSeq++
	w := widget{ID: fmt.Sprintf("w%d", s.nextSeq), Name: in.Name, Status: in.Status}
	s.items[w.ID] = w
	return w, nil
}

func (s *memStore) Update(_ context.Context, id string, in updateWidget) (widget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.items[id]
	if !ok {
		return widget{}, notFoundErr{id}
	}
	if in.Name != nil {
		w.Name = *in.Name
	}
	if in.Status != nil {
		w.Status = *in.Status
	}
	s.items[id] = w
	return w, nil
}

func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return notFoundErr{id}
	}
	delete(s.items, id)
	return nil
}

func newTestAuthorizer(t *testing.T, policy string) *authz.Authorizer {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.cedar"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	store, err := authz.LoadPolicyDir(dir)
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	roles, err := authz.NewRoleRegistry(authz.Role{Name: "viewer"}, authz.Role{Name: "admin"})
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}
	return authz.New(store, roles, nil)
}

func withPrincipal(req *http.Request, roles ...string) *http.Request {
	p := &authn.Principal{Subject: "alice", Roles: roles}
	return req.WithContext(authn.WithPrincipal(req.Context(), p))
}

func newRouter(t *testing.T, store *memStore, a *authz.Authorizer) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Mount("/widgets", crud.NewResource(crud.Config[widget, createWidget, updateWidget]{
		ResourceType:     "Widget",
		Store:            store,
		Authorizer:       a,
		SortableFields:   map[string]bool{"name": true},
		FilterableFields: map[string]bool{"status": true},
	}, true))
	return r
}

func TestResource_CRUDLifecycle_NoAuthorizer(t *testing.T) {
	store := newMemStore()
	r := newRouter(t, store, nil)

	// Create
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/widgets", strings.NewReader(`{"name":"Widget A","status":"active"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var created widget
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected server-assigned ID")
	}

	// Get
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Update (partial)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/widgets/"+created.ID, strings.NewReader(`{"status":"retired"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var updated widget
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Status != "retired" || updated.Name != "Widget A" {
		t.Fatalf("partial update changed wrong fields: %+v", updated)
	}

	// Delete
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/widgets/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Get after delete -> 500 from our notFoundErr not being a *problem.Problem;
	// still must not be 200.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets/"+created.ID, nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("expected deleted widget to be gone, got 200: %s", rec.Body.String())
	}
}

func TestResource_List_FilterSortAndCount(t *testing.T) {
	store := newMemStore()
	r := newRouter(t, store, nil)

	for _, w := range []createWidget{
		{Name: "Charlie", Status: "active"},
		{Name: "Alpha", Status: "active"},
		{Name: "Bravo", Status: "retired"},
	} {
		body, _ := json.Marshal(w)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/widgets", strings.NewReader(string(body))))
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed create failed: %s", rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets?status=active&sort=name&count=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var page struct {
		Data []widget `json:"data"`
		Meta struct {
			Total *int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("expected 2 active widgets, got %d: %+v", len(page.Data), page.Data)
	}
	if page.Data[0].Name != "Alpha" || page.Data[1].Name != "Charlie" {
		t.Fatalf("expected alphabetical order, got %+v", page.Data)
	}
	if page.Meta.Total == nil || *page.Meta.Total != 2 {
		t.Fatalf("expected total=2, got %+v", page.Meta.Total)
	}
}

func TestResource_List_RejectsUnfilterableField(t *testing.T) {
	store := newMemStore()
	r := newRouter(t, store, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets?secretField=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestResource_AuthzEnforced(t *testing.T) {
	store := newMemStore()
	a := newTestAuthorizer(t, `
permit (principal in Role::"viewer", action == Action::"widget:list", resource);
permit (principal in Role::"admin", action == Action::"widget:create", resource);
`)
	r := newRouter(t, store, a)

	// No principal at all -> 401.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}

	// Viewer can list.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, withPrincipal(httptest.NewRequest(http.MethodGet, "/widgets", nil), "viewer"))
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Viewer cannot create.
	rec = httptest.NewRecorder()
	req := withPrincipal(httptest.NewRequest(http.MethodPost, "/widgets", strings.NewReader(`{"name":"X"}`)), "viewer")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}

	// Admin can create.
	rec = httptest.NewRecorder()
	req = withPrincipal(httptest.NewRequest(http.MethodPost, "/widgets", strings.NewReader(`{"name":"X"}`)), "admin")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
