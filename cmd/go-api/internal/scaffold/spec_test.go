package scaffold

import (
	"strings"
	"testing"
)

func TestParseSpecValidation(t *testing.T) {
	cases := []struct {
		name          string
		svc           string
		module        string
		authn         string
		authz, db     bool
		cruds         []string
		wantErrSubstr string
	}{
		{name: "missing module", svc: "orders", module: "", authn: "hmac", wantErrSubstr: "-module is required"},
		{name: "bad service name", svc: "Orders", module: "github.com/acme/orders", authn: "hmac", wantErrSubstr: "service name"},
		{name: "bad authn mode", svc: "orders", module: "github.com/acme/orders", authn: "oauth", wantErrSubstr: "-authn must be one of"},
		{name: "crud without db", svc: "orders", module: "github.com/acme/orders", authn: "hmac", cruds: []string{"Order"}, db: false, wantErrSubstr: "-crud requires -db"},
		{name: "duplicate crud", svc: "orders", module: "github.com/acme/orders", authn: "hmac", db: true, cruds: []string{"Order", "order"}, wantErrSubstr: "more than once"},
		{name: "invalid crud name", svc: "orders", module: "github.com/acme/orders", authn: "hmac", db: true, cruds: []string{"line-item"}, wantErrSubstr: "must start with a letter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseSpec(c.svc, c.module, "", c.authn, c.authz, c.db, c.cruds, false, false)
			if err == nil {
				t.Fatalf("expected an error containing %q, got none", c.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), c.wantErrSubstr) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.wantErrSubstr)
			}
		})
	}
}

func TestParseSpecValid(t *testing.T) {
	spec, err := ParseSpec("orders", "github.com/acme/orders", "", "jwt", true, true, []string{"Order", "LineItem"}, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.Dir != "./orders" {
		t.Errorf("Dir = %q, want %q (default from name)", spec.Dir, "./orders")
	}
	if len(spec.Resources) != 2 || spec.Resources[0].Name != "Order" || spec.Resources[1].Name != "LineItem" {
		t.Errorf("Resources = %+v", spec.Resources)
	}
}
