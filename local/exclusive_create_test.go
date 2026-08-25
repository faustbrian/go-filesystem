package local

import (
	"context"
	"os"
	"strings"
	"testing"

	filesystem "github.com/faustbrian/go-filesystem"
)

func TestWriteUsesExclusiveCreateFlagsAndConfiguredMode(t *testing.T) {
	root := &fakeRoot{create: &fakeFile{}, statInfo: fakeInfo{name: "object", mode: 0o640}}
	adapter := fakeAdapter(root)
	path := filesystem.MustParsePath("object")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("content"), filesystem.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if root.openFlags != os.O_WRONLY|os.O_CREATE|os.O_EXCL {
		t.Fatalf("OpenFile flags = %d", root.openFlags)
	}
	if root.openMode != 0o600 {
		t.Fatalf("OpenFile mode = %o, want 600", root.openMode)
	}
}
