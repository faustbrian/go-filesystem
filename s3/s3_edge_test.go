package s3

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func TestPublicConstructorsAndOptions(t *testing.T) {
	t.Parallel()

	if adapter, err := New(nil, "bucket"); err == nil {
		_ = adapter
		t.Fatal("New(nil) error = nil")
	}
	if adapter, err := NewR2Transport(nil, "bucket"); err == nil {
		_ = adapter
		t.Fatal("NewR2Transport(nil) error = nil")
	}
	client := awss3.New(awss3.Options{
		Region:      "us-east-1",
		Credentials: aws.AnonymousCredentials{},
	})
	transferOption := func(options *transfermanager.Options) {
		options.PartSizeBytes = 8 * 1024 * 1024
	}
	adapter, err := New(
		client,
		"bucket",
		WithPrefix("tenant//objects"),
		WithMaxListEntries(25),
		WithMetadataLimits(16, 4*1024),
		WithTransferOptions(transferOption),
	)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.prefix != "tenant/objects" || adapter.maxList != 25 || adapter.maxMetadataEntries != 16 || adapter.maxMetadataBytes != 4*1024 || len(adapter.uploadOptions) != 1 {
		t.Fatalf("New() config = prefix %q list %d metadata %d/%d options %d", adapter.prefix, adapter.maxList, adapter.maxMetadataEntries, adapter.maxMetadataBytes, len(adapter.uploadOptions))
	}
	r2Adapter, err := NewR2Transport(client, "bucket")
	if err != nil || r2Adapter.adapterName != "r2" {
		t.Fatalf("NewR2Transport() = %+v, %v", r2Adapter, err)
	}
	for _, constructor := range []func(Option) error{
		func(option Option) error { _, err := New(client, "bucket", option); return err },
		func(option Option) error { _, err := NewR2Transport(client, "bucket", option); return err },
	} {
		if err := constructor(WithMaxListEntries(0)); err == nil {
			t.Fatal("constructor accepted an invalid maximum")
		}
		if err := constructor(WithMetadataLimits(0, 1)); err == nil {
			t.Fatal("constructor accepted invalid metadata entries")
		}
		if err := constructor(WithMetadataLimits(1, 0)); err == nil {
			t.Fatal("constructor accepted invalid metadata bytes")
		}
	}
}

func TestInternalConstructorRejectsDependenciesAndProfile(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	for _, test := range []struct {
		client    objectClient
		uploader  uploadClient
		presigner presignClient
	}{
		{uploader: backend, presigner: backend},
		{client: backend, presigner: backend},
		{client: backend, uploader: backend},
	} {
		if _, err := newAdapter(test.client, test.uploader, test.presigner, config{adapterName: "s3", bucket: "bucket", maxList: 1}); err == nil {
			t.Fatal("newAdapter() accepted a nil dependency")
		}
	}
	if _, err := newAdapter(backend, backend, backend, config{adapterName: "gcs", bucket: "bucket", maxList: 1}); err == nil {
		t.Fatal("newAdapter() accepted an invalid profile")
	}
	if _, err := newAdapter(backend, backend, backend, config{
		adapterName:        "s3",
		bucket:             "bucket",
		maxList:            1,
		maxMetadataEntries: 1,
	}); err == nil {
		t.Fatal("newAdapter() accepted incomplete metadata limits")
	}
}

func TestRangeAndWriteValidationAndFailures(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 10})
	path := filesystem.MustParsePath("object")
	for _, byteRange := range []filesystem.ByteRange{
		{Offset: -1, Length: 1},
		{Length: 0},
		{Offset: 2, Length: int64(^uint64(0) >> 1)},
	} {
		if _, err := adapter.OpenRange(context.Background(), path, byteRange); !errors.Is(err, filesystem.ErrInvalidRange) {
			t.Fatalf("OpenRange(%+v) error = %v", byteRange, err)
		}
	}
	backend.getErr = &smithy.GenericAPIError{Code: "InvalidRange"}
	if _, err := adapter.OpenRange(context.Background(), path, filesystem.ByteRange{Length: 1}); !errors.Is(err, filesystem.ErrInvalidRange) {
		t.Fatalf("OpenRange(remote) error = %v", err)
	}
	backend.getErr = nil
	if _, err := adapter.Write(context.Background(), filesystem.Root(), strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrInvalidPath) {
		t.Fatalf("Write(root) error = %v", err)
	}
	backend.uploadErr = &smithy.GenericAPIError{Code: "PreconditionFailed"}
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); !errors.Is(err, filesystem.ErrPreconditionFailed) {
		t.Fatalf("Write(upload failure) error = %v", err)
	}
	backend.uploadErr = nil
	backend.headErr = errors.New("head failed")
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{}); err == nil || err.Error() != "head failed" {
		t.Fatalf("Write(stat failure) error = %v", err)
	}
}

func TestOperationFailuresAreMapped(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 10})
	path := filesystem.MustParsePath("object")
	backend.getErr = &awstypes.NoSuchKey{}
	if _, err := adapter.Open(context.Background(), path); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Open() error = %v", err)
	}
	backend.headErr = &awstypes.NotFound{}
	if _, err := adapter.Stat(context.Background(), path); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Stat() error = %v", err)
	}
	backend.deleteErr = &awstypes.NoSuchKey{}
	if err := adapter.Delete(context.Background(), path); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Delete() error = %v", err)
	}
	backend.copyErr = &awstypes.NoSuchKey{}
	if err := adapter.Copy(context.Background(), path, filesystem.MustParsePath("copy"), filesystem.CopyOptions{}); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("Copy(no overwrite) error = %v", err)
	}
	if err := adapter.Copy(context.Background(), path, filesystem.MustParsePath("copy"), filesystem.CopyOptions{Overwrite: true}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("Copy() error = %v", err)
	}
	if err := adapter.SetVisibility(context.Background(), path, filesystem.VisibilityPublic); !errors.Is(err, filesystem.ErrUnsupportedCapability) {
		t.Fatalf("SetVisibility() error = %v", err)
	}
}

func TestListPaginationBoundsAndHostileKeys(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", prefix: "tenant", maxList: 4})
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List(negative limit) error = nil")
	}
	token := "next"
	backend.listOutputs = []*awss3.ListObjectsV2Output{
		{
			Contents: []awstypes.Object{
				{Key: aws.String("outside/file")},
				{Key: aws.String("tenant/a"), Size: aws.Int64(1)},
			},
			CommonPrefixes: []awstypes.CommonPrefix{
				{Prefix: aws.String("tenant/directory/")},
				{Prefix: aws.String("outside/")},
			},
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String(token),
		},
		{Contents: []awstypes.Object{{Key: aws.String("tenant/b"), Size: aws.Int64(2)}}},
	}
	iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if entry := iterator.Entry(); !entry.Path.IsRoot() {
		t.Fatalf("Entry(before Next) = %+v", entry)
	}
	var paths []string
	for iterator.Next() {
		paths = append(paths, iterator.Entry().Path.String())
	}
	if strings.Join(paths, ",") != "a,b,directory" {
		t.Fatalf("List() paths = %v", paths)
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
	}

	backend = newFakeBackend()
	backend.listOutputs = []*awss3.ListObjectsV2Output{{
		Contents:       []awstypes.Object{{Key: aws.String("tenant/a")}, {Key: aws.String("tenant/b")}},
		CommonPrefixes: []awstypes.CommonPrefix{{Prefix: aws.String("tenant/directory/")}},
	}}
	adapter = mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", prefix: "tenant", maxList: 1})
	iterator, err = adapter.List(context.Background(), filesystem.MustParsePath("subdirectory"), filesystem.ListOptions{Recursive: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iterator.Close() }()
	if !iterator.Next() || iterator.Next() {
		t.Fatal("maximum list bound was not enforced")
	}
	backend.listErr = &smithy.GenericAPIError{Code: "NoSuchBucket"}
	backend.listOutputs = nil
	backend.listCalls = 0
	if _, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("List(error) = %v", err)
	}
}

func TestMetadataAndTemporaryURLFailures(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 10})
	path := filesystem.MustParsePath("object")
	backend.headErr = errors.New("head failed")
	if err := adapter.SetMetadata(context.Background(), path, nil); err == nil || err.Error() != "head failed" {
		t.Fatalf("SetMetadata(stat) error = %v", err)
	}
	backend.headErr = nil
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	backend.copyErr = errors.New("copy failed")
	if err := adapter.SetMetadata(context.Background(), path, map[string]string{"key": "value"}); err == nil || err.Error() != "copy failed" {
		t.Fatalf("SetMetadata(copy) error = %v", err)
	}
	if _, err := adapter.TemporaryURL(context.Background(), path, time.Minute, filesystem.TemporaryURLOptions{DownloadName: "bad\nname"}); err == nil {
		t.Fatal("TemporaryURL(control name) error = nil")
	}
	backend.presignErr = context.DeadlineExceeded
	if _, err := adapter.TemporaryURL(context.Background(), path, time.Minute, filesystem.TemporaryURLOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("TemporaryURL(presign) error = %v", err)
	}
}

func TestMetadataLimitsBoundRequestsAndResponses(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{
		adapterName:        "s3",
		bucket:             "bucket",
		maxList:            10,
		maxMetadataEntries: 1,
		maxMetadataBytes:   8,
	})
	path := filesystem.MustParsePath("object")
	oversized := map[string]string{"first": "1", "second": "2"}
	if _, err := adapter.Write(context.Background(), path, strings.NewReader("x"), filesystem.WriteOptions{Metadata: oversized}); !errors.Is(err, filesystem.ErrResourceLimit) {
		t.Fatalf("Write(oversized metadata) error = %v", err)
	}
	if len(backend.objects) != 0 {
		t.Fatal("Write() reached backend with oversized metadata")
	}
	backend.objects["object"] = fakeObject{content: []byte("x"), metadata: oversized}
	if _, err := adapter.Stat(context.Background(), path); !errors.Is(err, filesystem.ErrResourceLimit) {
		t.Fatalf("Stat(oversized metadata) error = %v", err)
	}
	backend.objects["object"] = fakeObject{content: []byte("x")}
	if err := adapter.SetMetadata(context.Background(), path, map[string]string{"key": "value-too-large"}); !errors.Is(err, filesystem.ErrResourceLimit) {
		t.Fatalf("SetMetadata(oversized metadata) error = %v", err)
	}
}

func TestKeyLogicalPathAndErrorHelpers(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 1})
	path := filesystem.MustParsePath("object")
	if adapter.key(path) != "object" {
		t.Fatalf("key() = %q", adapter.key(path))
	}
	if logical, ok := adapter.logicalPath("directory/", true); !ok || logical.String() != "directory" {
		t.Fatalf("logicalPath(directory) = %q, %v", logical, ok)
	}
	if _, ok := adapter.logicalPath("", false); ok {
		t.Fatal("logicalPath(empty) succeeded")
	}

	for _, test := range []struct {
		err  error
		want error
	}{
		{err: nil, want: nil},
		{err: context.Canceled, want: context.Canceled},
		{err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{err: &smithy.GenericAPIError{Code: "NotFound"}, want: filesystem.ErrNotFound},
		{err: &smithy.GenericAPIError{Code: "RequestedRangeNotSatisfiable"}, want: filesystem.ErrInvalidRange},
		{err: errors.New("opaque"), want: nil},
	} {
		mapped := mapError(path, test.err)
		if test.want != nil && !errors.Is(mapped, test.want) {
			t.Fatalf("mapError(%v) = %v, want %v", test.err, mapped, test.want)
		}
		if test.want == nil && test.err != nil && !errors.Is(mapped, test.err) {
			t.Fatalf("mapError(%v) = %v", test.err, mapped)
		}
	}
	if !containsControl("tab\tname") || containsControl("plain-name") {
		t.Fatal("containsControl() classification is wrong")
	}
}

func TestMapErrorRedactsRemoteCredentials(t *testing.T) {
	t.Parallel()

	path := filesystem.MustParsePath("object")
	remote := errors.New(
		"request failed: https://user:password@example.test/object?" +
			"X-Amz-Credential=access-key&X-Amz-Signature=signature-secret " +
			"Authorization: Bearer authorization-secret",
	)
	mapped := mapError(path, remote)
	if !errors.Is(mapped, remote) {
		t.Fatalf("mapError() did not preserve the cause: %v", mapped)
	}
	for _, secret := range []string{
		"user",
		"password",
		"access-key",
		"signature-secret",
		"authorization-secret",
		"X-Amz-Credential",
	} {
		if strings.Contains(mapped.Error(), secret) {
			t.Fatalf("mapError() leaked %q: %v", secret, mapped)
		}
	}
}
