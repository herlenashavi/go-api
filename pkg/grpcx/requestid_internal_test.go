package grpcx

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestRequestIDFromMetadata_RejectsInvalid(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"valid kept":    {"upstream-id-123", "upstream-id-123"},
		"oversized":     {strings.Repeat("a", 129), ""},
		"control chars": {"abc\x01def", ""},
		"space":         {"abc def", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(requestIDMetadataKey, tc.in))
			if got := requestIDFromMetadata(ctx); got != tc.want {
				t.Fatalf("requestIDFromMetadata(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
