package s3

import (
	"strings"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func FuzzLogicalKeyTranslation(f *testing.F) {
	for _, seed := range []string{
		"tenant/object.txt",
		"tenant/directory/",
		"outside/object",
		"tenant/../escape",
		"tenant/bad\x00key",
	} {
		f.Add(seed, strings.HasSuffix(seed, "/"))
	}
	backend := newFakeBackend()
	adapter := mustFuzzAdapter(f, backend)
	f.Fuzz(func(t *testing.T, key string, directory bool) {
		logicalPath, ok := adapter.logicalPath(key, directory)
		if !ok {
			return
		}
		reparsed, err := filesystem.ParsePath(logicalPath.String())
		if err != nil || reparsed != logicalPath {
			t.Fatalf("logical path is not stable: %q, %v", reparsed, err)
		}
		if got := adapter.key(logicalPath); !strings.HasPrefix(got, "tenant/") {
			t.Fatalf("key(%q) escaped prefix: %q", logicalPath, got)
		}
	})
}

func mustFuzzAdapter(f *testing.F, backend *fakeBackend) *Adapter {
	f.Helper()
	adapter, err := newAdapter(backend, backend, backend, config{
		adapterName: "s3",
		bucket:      "bucket",
		prefix:      "tenant",
		maxList:     100,
	})
	if err != nil {
		f.Fatal(err)
	}
	return adapter
}
