package reqctx

import (
	"strings"
	"testing"
)

func TestValidRequestID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"uuid", "3f2b8c1e-6a4d-4e1b-9c2f-0d7e5a1b2c3d", true},
		{"dotted", "svc.a:123_b", true},
		{"max length", strings.Repeat("a", 128), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 129), false},
		{"newline", "abc\ndef", false},
		{"space", "abc def", false},
		{"quote", `abc"def`, false},
		{"equals", "Root=1-5759e988", false},
		{"non-ascii", "abcé", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidRequestID(tc.id); got != tc.want {
				t.Fatalf("ValidRequestID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
