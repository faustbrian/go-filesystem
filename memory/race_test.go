package memory_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func TestSharedAdapterStreamsConcurrently(t *testing.T) {
	t.Parallel()

	const workers = 32
	adapter := memory.New()
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			path := filesystem.MustParsePath(fmt.Sprintf("objects/%d", index))
			content := strings.Repeat(fmt.Sprintf("%02d", index), 4_096)
			if _, err := adapter.Write(
				context.Background(),
				path,
				strings.NewReader(content),
				filesystem.WriteOptions{},
			); err != nil {
				errors <- err
				return
			}
			stream, err := adapter.Open(context.Background(), path)
			if err != nil {
				errors <- err
				return
			}
			defer func() { _ = stream.Close() }()
			got, err := io.ReadAll(stream)
			if err != nil {
				errors <- err
				return
			}
			if string(got) != content {
				errors <- fmt.Errorf("content for %s was corrupted", path)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
