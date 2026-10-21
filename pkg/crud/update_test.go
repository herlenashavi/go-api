package crud_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/herlenashavi/go-api/pkg/middleware"
)

// TestResource_Update_OversizedBodyIs413 pins updateHandler to httpx's
// canonical body decoding: a body over BodyLimit's cap is a 413, exactly
// like a Handle-based route, not a generic 400 "not valid JSON".
func TestResource_Update_OversizedBodyIs413(t *testing.T) {
	store := newMemStore()
	store.items["w1"] = widget{ID: "w1", Name: "A"}
	h := middleware.BodyLimit(16)(newRouter(t, store, nil))

	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		rec := httptest.NewRecorder()
		body := `{"name":"` + strings.Repeat("x", 64) + `"}`
		h.ServeHTTP(rec, httptest.NewRequest(method, "/widgets/w1", strings.NewReader(body)))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s oversized body: status = %d, want 413, body=%s", method, rec.Code, rec.Body.String())
		}
	}
}

// TestResource_Update_EmptyBodyIsNotAJSONError: an empty PATCH is a no-op
// partial update, not "The request body is not valid JSON: EOF".
func TestResource_Update_EmptyBodyIsNotAJSONError(t *testing.T) {
	store := newMemStore()
	store.items["w1"] = widget{ID: "w1", Name: "A"}
	r := newRouter(t, store, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/widgets/w1", strings.NewReader("")))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty PATCH: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}
