// Package filesystem defines capability-based interfaces and shared types for
// storage adapters.
package filesystem

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// ErrInvalidPath identifies a logical path that is empty, escapes its logical
// root, or has platform-specific or otherwise ambiguous syntax.
var ErrInvalidPath = errors.New("invalid filesystem path")

// Path is a normalized, root-relative logical filesystem path.
//
// Paths always use forward slashes and never contain empty, current-directory,
// or parent-directory segments.
type Path struct {
	value string
}

// Root returns the logical root. ParsePath intentionally rejects an empty
// string so accidental empty object names cannot be confused with this value.
func Root() Path {
	return Path{}
}

// ParsePath validates and normalizes a logical filesystem path.
func ParsePath(value string) (Path, error) {
	if value == "" {
		return Path{}, invalidPath(value, "path is empty")
	}
	if strings.HasPrefix(value, `\\`) || hasWindowsVolume(value) {
		return Path{}, invalidPath(value, "platform-specific path")
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return Path{}, invalidPath(value, "control character")
		}
	}

	value = strings.ReplaceAll(value, `\`, "/")
	segments := strings.Split(value, "/")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			return Path{}, invalidPath(value, "parent segment")
		default:
			normalized = append(normalized, segment)
		}
	}
	if len(normalized) == 0 {
		return Path{}, invalidPath(value, "path has no segments")
	}
	normalizedValue := strings.Join(normalized, "/")
	if hasWindowsVolume(normalizedValue) {
		return Path{}, invalidPath(value, "platform-specific path")
	}

	return Path{value: normalizedValue}, nil
}

// MustParsePath is ParsePath that panics when value is invalid.
func MustParsePath(value string) Path {
	parsed, err := ParsePath(value)
	if err != nil {
		panic(err)
	}

	return parsed
}

// String returns the normalized logical path.
func (p Path) String() string {
	return p.value
}

// IsRoot reports whether p represents the logical root.
func (p Path) IsRoot() bool {
	return p.value == ""
}

// Base returns the final path segment.
func (p Path) Base() string {
	return path.Base(p.value)
}

// Dir returns the path containing p. For a top-level path, Dir returns the
// zero Path, which represents the logical root for relationship operations.
func (p Path) Dir() Path {
	directory := path.Dir(p.value)
	if directory == "." {
		return Path{}
	}

	return Path{value: directory}
}

// Join appends a relative logical path to p.
func (p Path) Join(relative string) (Path, error) {
	joined, err := ParsePath(relative)
	if err != nil {
		return Path{}, err
	}
	if p.value == "" {
		return joined, nil
	}

	return ParsePath(p.value + "/" + joined.value)
}

func invalidPath(value, reason string) error {
	return fmt.Errorf("%w %q: %s", ErrInvalidPath, value, reason)
}

func hasWindowsVolume(value string) bool {
	return len(value) >= 2 && value[1] == ':' &&
		((value[0] >= 'A' && value[0] <= 'Z') ||
			(value[0] >= 'a' && value[0] <= 'z'))
}
