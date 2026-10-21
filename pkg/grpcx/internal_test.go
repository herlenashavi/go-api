package grpcx

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"net/url"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestPeerIdentityName(t *testing.T) {
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	cases := []struct {
		name string
		uris []*url.URL
		want string
	}{
		{"spiffe URI wins", []*url.URL{mustURL("spiffe://example.org/ns/default/sa/heroes")}, "spiffe://example.org/ns/default/sa/heroes"},
		{"scheme merely prefixed by spiffe falls back to CN", []*url.URL{mustURL("spiffex://example.org/heroes")}, "heroes-cn"},
		{"no URIs falls back to CN", nil, "heroes-cn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := &x509.Certificate{Subject: pkix.Name{CommonName: "heroes-cn"}, URIs: tc.uris}
			if got := peerIdentityName(cert); got != tc.want {
				t.Fatalf("peerIdentityName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBearerTokenFromMetadata_SchemeIsCaseInsensitive(t *testing.T) {
	for _, header := range []string{"Bearer abc", "bearer abc", "BEARER abc", "bEaReR  abc "} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", header))
		got, err := bearerTokenFromMetadata(ctx)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", header, err)
		}
		if got != "abc" {
			t.Fatalf("%q: token = %q, want %q", header, got, "abc")
		}
	}

	for _, header := range []string{"Basic abc", "Bearer", "Bearer   ", "Bear abc"} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", header))
		if _, err := bearerTokenFromMetadata(ctx); err == nil {
			t.Fatalf("%q: expected an error", header)
		}
	}
}
