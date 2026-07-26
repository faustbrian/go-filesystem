package filesystem_test

import (
	"errors"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func TestParsePathNormalizesLogicalPaths(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"file.txt":             "file.txt",
		"directory/file.txt":   "directory/file.txt",
		"repeated separators":  "directory/file.txt",
		"dot segments":         "directory/file.txt",
		"leading separator":    "directory/file.txt",
		"backslash separators": "directory/file.txt",
		"unicode path":         "café/資料.txt",
	}

	inputs := map[string]string{
		"file.txt":             "file.txt",
		"directory/file.txt":   "directory/file.txt",
		"repeated separators":  "directory//file.txt",
		"dot segments":         "directory/./file.txt",
		"leading separator":    "/directory/file.txt",
		"backslash separators": `directory\file.txt`,
		"unicode path":         "café/資料.txt",
	}

	for name, want := range tests {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := filesystem.ParsePath(inputs[name])
			if err != nil {
				t.Fatalf("ParsePath() error = %v", err)
			}
			if got.String() != want {
				t.Fatalf("ParsePath().String() = %q, want %q", got, want)
			}
		})
	}
}

func TestParsePathRejectsAmbiguousOrEscapingPaths(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		".",
		"..",
		"../file.txt",
		"directory/../../file.txt",
		"directory/../file.txt",
		`directory\..\file.txt`,
		"directory/\x00/file.txt",
		"C:/windows/path.txt",
		`\\server\share\file.txt`,
	}

	for _, input := range tests {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := filesystem.ParsePath(input)
			if !errors.Is(err, filesystem.ErrInvalidPath) {
				t.Fatalf("ParsePath(%q) error = %v, want ErrInvalidPath", input, err)
			}
		})
	}
}

func TestPathRelationshipOperations(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("directory/nested/file.txt")
	if got := path.Base(); got != "file.txt" {
		t.Fatalf("Base() = %q, want file.txt", got)
	}
	if got := path.Dir().String(); got != "directory/nested" {
		t.Fatalf("Dir() = %q, want directory/nested", got)
	}

	joined, err := filesystem.ParsePath("directory")
	if err != nil {
		t.Fatal(err)
	}
	joined, err = joined.Join("nested/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if joined != path {
		t.Fatalf("Join() = %q, want %q", joined, path)
	}

	if _, err := joined.Join("../escape.txt"); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Join() error = %v, want ErrInvalidPath", err)
	}
}

func TestRootPathSupportsRelationshipOperations(t *testing.T) {
	t.Parallel()

	root := filesystem.Root()
	if !root.IsRoot() {
		t.Fatal("Root().IsRoot() = false, want true")
	}
	if root.String() != "" {
		t.Fatalf("Root().String() = %q, want empty string", root)
	}

	joined, err := root.Join("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if joined.String() != "file.txt" {
		t.Fatalf("Root().Join() = %q, want file.txt", joined)
	}
	if !joined.Dir().IsRoot() {
		t.Fatal("top-level path Dir().IsRoot() = false, want true")
	}
}

func TestMustParsePathPanicsForInvalidPath(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("MustParsePath() did not panic")
		}
	}()

	filesystem.MustParsePath("../escape.txt")
}
