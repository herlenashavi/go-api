// Package httpx is go-api's typed handler wrapper — the framework's answer
// to "how do handlers declare their contract" without adopting a large
// reflection-based validation framework. A handler is a plain Go function:
//
//	func get(ctx context.Context, in *GetHeroInput) (*GetHeroOutput, error)
//
// Request structs use three struct tags to pull values out of the request
// before the rest of the struct is JSON-decoded as the body:
//
//	type GetHeroInput struct {
//		ID     uuid.UUID `path:"id"`
//		Expand bool      `query:"expand"`
//	}
//
// This is bounded, single-purpose reflection over your own request types.
// It reads tags once per request to populate a few fields; it never
// generates types, mutates package state, or needs special-cased escape
// hatches.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// Decode populates a new *In from r: path params (tag `path`), query params
// (tag `query`), header values (tag `header`), and finally the JSON body
// into whatever's left. An absent body is not an error — GET/DELETE
// requests routinely have none.
func Decode[In any](r *http.Request) (*In, error) {
	var in In

	if err := decodeBody(r, &in); err != nil {
		return nil, err
	}
	if err := bindTagged(r, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func decodeBody(r *http.Request, in any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(in); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // no body — fine for GET/DELETE and bodyless POSTs
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return problem.New(http.StatusRequestEntityTooLarge, "payload-too-large",
				"Payload Too Large", fmt.Sprintf("Request body exceeds the %d byte limit.", maxErr.Limit))
		}
		return problem.BadRequest("The request body is not valid JSON: " + err.Error())
	}
	return nil
}

// bindTagged fills fields tagged `path`, `query`, or `header` from the
// request, overwriting whatever decodeBody may have set for them — path,
// query, and header values are the source of truth for the fields that
// declare them.
func bindTagged(r *http.Request, in any) error {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("httpx: Decode requires a struct type, got %T", in)
	}
	v = v.Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := v.Field(i)

		if name, ok := field.Tag.Lookup("path"); ok {
			raw := chi.URLParam(r, name)
			if raw == "" {
				continue
			}
			if err := setField(fv, field, []string{raw}); err != nil {
				return problem.BadRequest(fmt.Sprintf("path parameter %q: %v", name, err))
			}
			continue
		}

		if name, ok := field.Tag.Lookup("query"); ok {
			values := r.URL.Query()[name]
			if len(values) == 0 {
				if def, ok := field.Tag.Lookup("default"); ok {
					values = []string{def}
				} else {
					continue
				}
			}
			if err := setField(fv, field, values); err != nil {
				return problem.BadRequest(fmt.Sprintf("query parameter %q: %v", name, err))
			}
			continue
		}

		if name, ok := field.Tag.Lookup("header"); ok {
			raw := r.Header.Get(name)
			if raw == "" {
				continue
			}
			if err := setField(fv, field, []string{raw}); err != nil {
				return problem.BadRequest(fmt.Sprintf("header %q: %v", name, err))
			}
			continue
		}
	}
	return nil
}

// setField converts values (one, unless the field is a slice and the source
// is a repeated query param) into fv according to its static type.
// Supported kinds: string, the sized int/uint/float kinds, bool,
// uuid.UUID, time.Time (RFC 3339), []string, and pointer-to-any-of-those
// (nil when absent, populated otherwise — the way to express an optional
// field).
func setField(fv reflect.Value, field reflect.StructField, values []string) error {
	if fv.Kind() == reflect.Pointer {
		elem := reflect.New(fv.Type().Elem())
		if err := setScalarOrSlice(elem.Elem(), field, values); err != nil {
			return err
		}
		fv.Set(elem)
		return nil
	}
	return setScalarOrSlice(fv, field, values)
}

func setScalarOrSlice(fv reflect.Value, field reflect.StructField, values []string) error {
	if fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() == reflect.String {
		if len(values) == 1 && strings.Contains(values[0], ",") {
			values = strings.Split(values[0], ",")
		}
		fv.Set(reflect.ValueOf(values))
		return nil
	}

	raw := values[0]

	switch {
	case fv.Type() == reflect.TypeOf(uuid.UUID{}):
		id, err := uuid.Parse(raw)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(id))
		return nil
	case fv.Type() == reflect.TypeOf(time.Time{}):
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(f)
	default:
		return fmt.Errorf("unsupported field type %s for %s", fv.Type(), field.Name)
	}
	return nil
}
