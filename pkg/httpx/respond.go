package httpx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// Respond writes v as the JSON response body with the given status.
func Respond(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Page is the standard envelope for list endpoints: `data` plus pagination
// metadata. Cursor-based pagination is the default (NextCursor is the
// opaque token for the next page, "" when there are no more); Total is
// populated only when the caller explicitly requested a count, since
// counting is often an expensive extra query best made opt-in.
type Page[T any] struct {
	Data []T  `json:"data"`
	Meta Meta `json:"meta"`
}

// Meta carries pagination metadata for a Page.
type Meta struct {
	NextCursor string `json:"nextCursor,omitempty"`
	// Offset/Limit echo the request's offset-pagination parameters when
	// that fallback mode was used instead of a cursor (see
	// pkg/crud/pagination.go) — omitted for cursor-paginated responses.
	Offset *int `json:"offset,omitempty"`
	Limit  *int `json:"limit,omitempty"`
	Total  *int `json:"total,omitempty"`
}

// EncodeCursor/DecodeCursor turn an opaque pagination cursor into a
// URL-safe string and back. The encoded value is base64 of raw bytes the
// caller controls (e.g. a last-seen ID or a composite sort key) — go-api
// does not interpret it, so your store package decides its shape.
func EncodeCursor(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCursor(cursor string) ([]byte, error) {
	if cursor == "" {
		return nil, nil
	}
	return base64.RawURLEncoding.DecodeString(cursor)
}
