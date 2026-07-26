package filesystem_test

import (
	"context"
	"fmt"
	"io"
	"strings"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/memory"
)

func Example() {
	ctx := context.Background()
	store := memory.New()
	logicalPath := filesystem.MustParsePath("documents/report.txt")
	_, err := store.Write(ctx, logicalPath, strings.NewReader("report"), filesystem.WriteOptions{
		ContentType: "text/plain",
	})
	if err != nil {
		panic(err)
	}
	stream, err := store.Open(ctx, logicalPath)
	if err != nil {
		panic(err)
	}
	defer func() { _ = stream.Close() }()
	content, err := io.ReadAll(stream)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(content))
	// Output: report
}
