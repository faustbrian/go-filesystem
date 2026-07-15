package filesystem_test

import (
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
)

func FuzzParsePath(f *testing.F) {
	for _, seed := range []string{
		"object.txt",
		"nested/object.txt",
		"../escape",
		`C:\\windows`,
		"unicode/雪.txt",
		"double//separator",
		"nul\x00byte",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		parsed, err := filesystem.ParsePath(raw)
		if err != nil {
			return
		}
		if parsed.IsRoot() || parsed.String() == "" {
			t.Fatal("ParsePath accepted a logical root")
		}
		if strings.Contains(parsed.String(), `\`) || strings.Contains(parsed.String(), "//") {
			t.Fatalf("ParsePath(%q) produced ambiguous path %q", raw, parsed)
		}
		for _, segment := range strings.Split(parsed.String(), "/") {
			if segment == "" || segment == "." || segment == ".." {
				t.Fatalf("ParsePath(%q) produced invalid segment in %q", raw, parsed)
			}
		}
		reparsed, err := filesystem.ParsePath(parsed.String())
		if err != nil || reparsed != parsed {
			t.Fatalf("normalized path is not idempotent: %q, %v", reparsed, err)
		}
	})
}
