package scaffold

import (
	"embed"
	"fmt"
	"go/format"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// render executes the named template (its base filename, e.g. "main.go")
// against data and, for a .go file, runs the result through go/format —
// not for style (gofmt's job is layout, and getting that wrong just looks
// ugly), but because it's the cheapest way to catch a template bug that
// produced syntactically invalid Go before it ever reaches disk.
func render(name string, data any) (string, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name+".tmpl", data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	out := b.String()

	if strings.HasSuffix(name, ".go") {
		formatted, err := format.Source([]byte(out))
		if err != nil {
			return "", fmt.Errorf("generated %s is not valid Go (this is a scaffold template bug, not yours): %w", name, err)
		}
		return string(formatted), nil
	}
	return out, nil
}
