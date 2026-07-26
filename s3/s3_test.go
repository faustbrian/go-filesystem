package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
)

func TestConformance(t *testing.T) {
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		backend := newFakeBackend()
		adapter, err := newAdapter(backend, backend, backend, config{
			adapterName: "s3",
			bucket:      "bucket",
			prefix:      "tenant",
			maxList:     100,
		})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
}

func TestR2TransportConformance(t *testing.T) {
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		backend := newFakeBackend()
		adapter, err := newAdapter(backend, backend, backend, config{
			adapterName: "r2",
			bucket:      "bucket",
			prefix:      "tenant",
			maxList:     100,
		})
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	tests := []config{
		{adapterName: "s3", bucket: "", maxList: 1},
		{adapterName: "s3", bucket: "bucket", prefix: "../escape", maxList: 1},
		{adapterName: "s3", bucket: "bucket", maxList: 0},
	}
	for _, configuration := range tests {
		configuration := configuration
		t.Run(fmt.Sprintf("%+v", configuration), func(t *testing.T) {
			t.Parallel()
			if _, err := newAdapter(backend, backend, backend, configuration); err == nil {
				t.Fatal("newAdapter() error = nil")
			}
		})
	}
}

func TestWriteStreamsToPrefixedKeyAndPreservesProperties(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{
		adapterName: "s3",
		bucket:      "bucket",
		prefix:      "tenant/uploads",
		maxList:     100,
	})
	path := filesystem.MustParsePath("file.txt")
	metadata, err := adapter.Write(
		context.Background(),
		path,
		strings.NewReader("streamed content"),
		filesystem.WriteOptions{
			ContentType: "text/plain",
			Metadata:    map[string]string{"owner": "test"},
			IfNoneMatch: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Path != path || metadata.Size != int64(len("streamed content")) {
		t.Fatalf("Write() metadata = %+v", metadata)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	stored := backend.objects["tenant/uploads/file.txt"]
	if string(stored.content) != "streamed content" || stored.contentType != "text/plain" {
		t.Fatalf("stored object = %+v", stored)
	}
	if stored.metadata["owner"] != "test" || !backend.lastIfNoneMatch {
		t.Fatalf("stored metadata = %v, if-none-match = %v", stored.metadata, backend.lastIfNoneMatch)
	}
}

func TestMoveIsTypedUnsupportedRatherThanCopyDeleteEmulation(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 100})
	if adapter.Capabilities().Supports(filesystem.CapabilityMove) {
		t.Fatal("Capabilities().Supports(move) = true")
	}
	err := adapter.Move(
		context.Background(),
		filesystem.MustParsePath("source"),
		filesystem.MustParsePath("destination"),
		filesystem.MoveOptions{},
	)
	if !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Move() error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestTemporaryURLUsesBoundedExpiryAndDownloadProperties(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", prefix: "tenant", maxList: 100})
	path := filesystem.MustParsePath("report.csv")
	got, err := adapter.TemporaryURL(
		context.Background(),
		path,
		15*time.Minute,
		filesystem.TemporaryURLOptions{DownloadName: "download.csv", ContentType: "text/csv"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://signed.example/tenant/report.csv" {
		t.Fatalf("TemporaryURL() = %q", got)
	}
	if backend.presignExpiry != 15*time.Minute || backend.presignKey != "tenant/report.csv" {
		t.Fatalf("presign expiry = %v, key = %q", backend.presignExpiry, backend.presignKey)
	}
	if backend.responseContentDisposition != "attachment; filename=download.csv" || backend.responseContentType != "text/csv" {
		t.Fatalf("presign response properties = %q, %q", backend.responseContentDisposition, backend.responseContentType)
	}

	for _, invalid := range []time.Duration{0, -time.Second, 7*24*time.Hour + time.Second} {
		if _, err := adapter.TemporaryURL(context.Background(), path, invalid, filesystem.TemporaryURLOptions{}); err == nil {
			t.Fatalf("TemporaryURL(%v) error = nil", invalid)
		}
	}
}

func TestCapabilitiesExposeObjectStoreGuarantees(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 100})
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityRead,
		filesystem.CapabilityWrite,
		filesystem.CapabilityDelete,
		filesystem.CapabilityList,
		filesystem.CapabilityStat,
		filesystem.CapabilityCopy,
		filesystem.CapabilityRangeRead,
		filesystem.CapabilityMetadata,
		filesystem.CapabilityTemporaryURL,
		filesystem.CapabilityMultipart,
	} {
		if !adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = false", capability)
		}
	}
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityMove,
		filesystem.CapabilityVisibility,
		filesystem.CapabilityChecksum,
	} {
		if adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = true", capability)
		}
	}
}

func mustAdapter(t *testing.T, backend *fakeBackend, configuration config) *Adapter {
	t.Helper()
	adapter, err := newAdapter(backend, backend, backend, configuration)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

type fakeObject struct {
	content     []byte
	contentType string
	metadata    map[string]string
	modified    time.Time
	etag        string
}

type fakeBackend struct {
	mu                         sync.Mutex
	objects                    map[string]fakeObject
	lastIfNoneMatch            bool
	presignExpiry              time.Duration
	presignKey                 string
	responseContentDisposition string
	responseContentType        string
	uploadErr                  error
	getErr                     error
	headErr                    error
	deleteErr                  error
	copyErr                    error
	listErr                    error
	presignErr                 error
	listOutputs                []*awss3.ListObjectsV2Output
	listCalls                  int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{objects: make(map[string]fakeObject)}
}

func (f *fakeBackend) UploadObject(_ context.Context, input *transfermanager.UploadObjectInput, options ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	for _, option := range options {
		option(&transfermanager.Options{})
	}
	content, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	key := aws.ToString(input.Key)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIfNoneMatch = aws.ToString(input.IfNoneMatch) == "*"
	if f.lastIfNoneMatch {
		if _, exists := f.objects[key]; exists {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
		}
	}
	etag := `"etag-` + strconv.Itoa(len(content)) + `"`
	f.objects[key] = fakeObject{
		content:     append([]byte(nil), content...),
		contentType: aws.ToString(input.ContentType),
		metadata:    cloneStringMap(input.Metadata),
		modified:    time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC),
		etag:        etag,
	}
	return &transfermanager.UploadObjectOutput{ETag: aws.String(etag)}, nil
}

func (f *fakeBackend) GetObject(_ context.Context, input *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, found := f.objects[aws.ToString(input.Key)]
	if !found {
		return nil, &awstypes.NoSuchKey{}
	}
	content := stored.content
	if value := aws.ToString(input.Range); value != "" {
		var start, end int
		if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || start >= len(content) {
			return nil, errors.New("invalid range")
		}
		if end >= len(content) {
			end = len(content) - 1
		}
		content = content[start : end+1]
	}
	return &awss3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(append([]byte(nil), content...))),
		ContentLength: aws.Int64(int64(len(content))),
		ContentType:   aws.String(stored.contentType),
		Metadata:      cloneStringMap(stored.metadata),
		ETag:          aws.String(stored.etag),
		LastModified:  aws.Time(stored.modified),
	}, nil
}

func (f *fakeBackend) HeadObject(_ context.Context, input *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	if f.headErr != nil {
		return nil, f.headErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, found := f.objects[aws.ToString(input.Key)]
	if !found {
		return nil, &awstypes.NotFound{}
	}
	return &awss3.HeadObjectOutput{
		ContentLength: aws.Int64(int64(len(stored.content))),
		ContentType:   aws.String(stored.contentType),
		Metadata:      cloneStringMap(stored.metadata),
		ETag:          aws.String(stored.etag),
		LastModified:  aws.Time(stored.modified),
	}, nil
}

func (f *fakeBackend) DeleteObject(_ context.Context, input *awss3.DeleteObjectInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(input.Key)
	if _, found := f.objects[key]; !found {
		return nil, &awstypes.NoSuchKey{}
	}
	delete(f.objects, key)
	return &awss3.DeleteObjectOutput{}, nil
}

func (f *fakeBackend) CopyObject(_ context.Context, input *awss3.CopyObjectInput, _ ...func(*awss3.Options)) (*awss3.CopyObjectOutput, error) {
	if f.copyErr != nil {
		return nil, f.copyErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	source, err := url.PathUnescape(aws.ToString(input.CopySource))
	if err != nil {
		return nil, err
	}
	source = strings.TrimPrefix(source, "bucket/")
	stored, found := f.objects[source]
	if !found {
		return nil, &awstypes.NoSuchKey{}
	}
	if input.MetadataDirective == awstypes.MetadataDirectiveReplace {
		stored.metadata = cloneStringMap(input.Metadata)
		stored.contentType = aws.ToString(input.ContentType)
	}
	stored.content = append([]byte(nil), stored.content...)
	f.objects[aws.ToString(input.Key)] = stored
	return &awss3.CopyObjectOutput{}, nil
}

func (f *fakeBackend) ListObjectsV2(_ context.Context, input *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listCalls < len(f.listOutputs) {
		output := f.listOutputs[f.listCalls]
		f.listCalls++
		return output, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := aws.ToString(input.Prefix)
	delimiter := aws.ToString(input.Delimiter)
	var keys []string
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	output := &awss3.ListObjectsV2Output{}
	prefixes := make(map[string]struct{})
	for _, key := range keys {
		remaining := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if index := strings.Index(remaining, delimiter); index >= 0 {
				prefixes[prefix+remaining[:index+1]] = struct{}{}
				continue
			}
		}
		stored := f.objects[key]
		output.Contents = append(output.Contents, awstypes.Object{
			Key:          aws.String(key),
			Size:         aws.Int64(int64(len(stored.content))),
			LastModified: aws.Time(stored.modified),
		})
	}
	for commonPrefix := range prefixes {
		output.CommonPrefixes = append(output.CommonPrefixes, awstypes.CommonPrefix{Prefix: aws.String(commonPrefix)})
	}
	sort.Slice(output.CommonPrefixes, func(left, right int) bool {
		return aws.ToString(output.CommonPrefixes[left].Prefix) < aws.ToString(output.CommonPrefixes[right].Prefix)
	})
	return output, nil
}

func (f *fakeBackend) PresignGetObject(_ context.Context, input *awss3.GetObjectInput, options ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	if f.presignErr != nil {
		return nil, f.presignErr
	}
	configuration := awss3.PresignOptions{}
	for _, option := range options {
		option(&configuration)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presignExpiry = configuration.Expires
	f.presignKey = aws.ToString(input.Key)
	f.responseContentDisposition = aws.ToString(input.ResponseContentDisposition)
	f.responseContentType = aws.ToString(input.ResponseContentType)
	return &v4.PresignedHTTPRequest{URL: "https://signed.example/" + f.presignKey}, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
