package scaffold

import "testing"

func TestNewResource(t *testing.T) {
	cases := []struct {
		name       string
		wantName   string
		wantLower  string
		wantPlural string
	}{
		{"Order", "Order", "order", "orders"},
		{"order", "Order", "order", "orders"},
		{"Category", "Category", "category", "categories"},
		{"Box", "Box", "box", "boxes"},
		{"Bus", "Bus", "bus", "buses"},
		{"Widget", "Widget", "widget", "widgets"},
	}
	for _, c := range cases {
		r, err := NewResource(c.name)
		if err != nil {
			t.Fatalf("NewResource(%q): %v", c.name, err)
		}
		if r.Name != c.wantName || r.Lower != c.wantLower || r.Plural != c.wantPlural {
			t.Errorf("NewResource(%q) = %+v, want {%s %s %s}", c.name, r, c.wantName, c.wantLower, c.wantPlural)
		}
	}
}

func TestNewResourceRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "line-item", "line_item", "123Order", "Order!"} {
		if _, err := NewResource(name); err == nil {
			t.Errorf("NewResource(%q): expected an error, got none", name)
		}
	}
}
