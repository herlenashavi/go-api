package scaffold

import (
	"sort"
	"strings"
)

// buildImportBlock renders the body of an `import (...)` block from two
// unsorted sets of import paths, grouped stdlib-then-everything-else with
// a blank line between — the same grouping gofmt preserves (and would
// itself produce via goimports) rather than something main.go.tmpl has to
// get right with per-flag conditionals. Each caller passes exactly the
// paths its generated file actually references; getting that set right is
// the generator's job precisely because Go, unlike gofmt, treats an
// unused import as a compile error it won't silently fix.
func buildImportBlock(stdlib, rest []string) string {
	var b strings.Builder
	writeGroup(&b, stdlib)
	if len(stdlib) > 0 && len(rest) > 0 {
		b.WriteString("\n")
	}
	writeGroup(&b, rest)
	return b.String()
}

func writeGroup(b *strings.Builder, paths []string) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for _, p := range sorted {
		b.WriteString("\t\"")
		b.WriteString(p)
		b.WriteString("\"\n")
	}
}
