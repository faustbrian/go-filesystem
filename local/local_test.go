package local_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	"github.com/faustbrian/golib/pkg/filesystem/local"
)

func TestConformance(t *testing.T) {
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		adapter, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := adapter.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return adapter
	})
}

func TestRootEscapeThroughSymlinkIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	_, err = adapter.Open(context.Background(), filesystem.MustParsePath("escape/secret.txt"))
	if err == nil {
		t.Fatal("Open() error = nil, want symlink policy error")
	}
}

func TestInternalSymlinkCanBeEnabledExplicitly(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target", "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	adapter, err := local.New(root, local.WithSymlinkPolicy(local.AllowInternalSymlinks))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	stream, err := adapter.Open(context.Background(), filesystem.MustParsePath("link/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	content, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Fatalf("Open() content = %q", content)
	}
}

func TestFailedWritePreservesExistingObjectAndCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	adapter, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	path := filesystem.MustParsePath("object.txt")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("original"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}

	_, err = adapter.Write(context.Background(), path, failingReader{}, filesystem.WriteOptions{})
	if err == nil {
		t.Fatal("Write() error = nil, want stream error")
	}
	content, err := os.ReadFile(filepath.Join(root, "object.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original" {
		t.Fatalf("stored content = %q, want original", content)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "object.txt" {
		t.Fatalf("root entries = %v, want only object.txt", entries)
	}
}

type failingReader struct{}

func (failingReader) Read(buffer []byte) (int, error) {
	copy(buffer, "partial")
	return len("partial"), errors.New("injected stream failure")
}
