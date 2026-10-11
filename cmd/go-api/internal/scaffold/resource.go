package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

// Resource is one --crud flag's worth of a generated service: a CRUD
// resource with a minimal, generic shape (id/name/createdAt/modifiedAt) —
// a starting point to edit, not a guess at your actual domain model.
type Resource struct {
	Name   string // Go type name, e.g. "Order" — exported, as given on the CLI but capitalized.
	Lower  string // unexported variable prefix, e.g. "order"
	Plural string // route segment / table name, e.g. "orders"
}

var resourceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// NewResource validates name and derives Lower/Plural from it. name must be
// a plain alphanumeric identifier — no underscores/hyphens, since it
// becomes a Go type name directly.
func NewResource(name string) (Resource, error) {
	if !resourceNamePattern.MatchString(name) {
		return Resource{}, fmt.Errorf("resource name %q must start with a letter and contain only letters/digits", name)
	}
	exported := strings.ToUpper(name[:1]) + name[1:]
	lower := strings.ToLower(name[:1]) + name[1:]
	return Resource{
		Name:   exported,
		Lower:  lower,
		Plural: strings.ToLower(pluralize(exported)),
	}, nil
}

// pluralize is a deliberately naive English pluralizer — good enough for
// the common case (Order -> orders, Category -> categories, Box -> boxes)
// and wrong often enough (Person -> persons) that the generated model file
// says so in a comment next to where Plural is used, so nobody mistakes
// this for a real inflection library. Edit the route/table name by hand
// for anything irregular; it's a generated starting point, not a contract.
func pluralize(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "y") && len(lower) > 1 && !isVowel(lower[len(lower)-2]):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "x"), strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}
