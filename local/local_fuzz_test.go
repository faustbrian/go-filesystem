package local_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/local"
)

func FuzzSymlinkContainment(f *testing.F) {
	for _, seed := range []string{
		"secret.txt",
		"../secret.txt",
		`C:\\secret.txt`,
		`\\server\\share`,
		"雪.txt",
		"nested//object",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, tail string) {
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "pivot")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		adapter, err := local.New(root)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = adapter.Close() }()
		logicalPath, err := filesystem.ParsePath("pivot/" + tail)
		if err != nil {
			return
		}
		stream, err := adapter.Open(context.Background(), logicalPath)
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		content, err := io.ReadAll(stream)
		if err == nil && string(content) == "outside-secret" {
			t.Fatalf("Open(%q) escaped the configured root", logicalPath)
		}
	})
}
