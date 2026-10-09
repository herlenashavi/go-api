package authz

import (
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
)

// Attrs converts a plain Go map into the cedar.RecordMap Authorizer.Check
// expects for resourceAttrs, so call sites doing row-level ABAC don't need
// their own cedar-go type conversions for the common scalar cases. Values
// of an unsupported type are dropped (not errored) rather than failing the
// whole authorization call over one bad attribute — log and fix it, don't
// let it turn into an outage.
func Attrs(values map[string]any) cedar.RecordMap {
	out := make(cedar.RecordMap, len(values))
	for k, v := range values {
		switch val := v.(type) {
		case string:
			out[cedar.String(k)] = cedar.String(val)
		case bool:
			out[cedar.String(k)] = cedar.Boolean(val)
		case int:
			out[cedar.String(k)] = cedar.Long(val)
		case int64:
			out[cedar.String(k)] = cedar.Long(val)
		case fmt.Stringer:
			out[cedar.String(k)] = cedar.String(val.String())
		}
	}
	return out
}
