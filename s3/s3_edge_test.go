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

func TestConfigurationRangeListAndMetadataBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		entries int
		bytes   int64
		valid   bool
	}{
		{entries: -1, bytes: 1},
		{entries: 0, bytes: 1},
		{entries: 1, bytes: -1},
		{entries: 1, bytes: 0},
		{entries: 1, bytes: 1, valid: true},
	} {
		if err := validateMetadataLimits(test.entries, test.bytes); (err == nil) != test.valid {
			t.Errorf("validateMetadataLimits(%d, %d) = %v", test.entries, test.bytes, err)
		}
	}

	maximum := int64(^uint64(0) >> 1)
	for _, test := range []struct {
		byteRange filesystem.ByteRange
		end       int64
		valid     bool
	}{
		{byteRange: filesystem.ByteRange{Offset: -1, Length: 1}},
		{byteRange: filesystem.ByteRange{Length: 0}},
		{byteRange: filesystem.ByteRange{Length: 1}, end: 0, valid: true},
		{byteRange: filesystem.ByteRange{Offset: 1, Length: 2}, end: 2, valid: true},
		{byteRange: filesystem.ByteRange{Offset: maximum, Length: 1}, end: maximum, valid: true},
		{byteRange: filesystem.ByteRange{Offset: maximum, Length: 2}},
	} {
		end, valid := inclusiveRangeEnd(test.byteRange)
		if end != test.end || valid != test.valid {
			t.Errorf("inclusiveRangeEnd(%+v) = %d, %t; want %d, %t", test.byteRange, end, valid, test.end, test.valid)
		}
	}

	for _, test := range []struct {
		requested int
		want      int
	}{
		{requested: 0, want: 100},
		{requested: 1, want: 1},
		{requested: 100, want: 100},
		{requested: 101, want: 100},
	} {
		if got := effectiveListLimit(test.requested, 100); got != test.want {
			t.Errorf("effectiveListLimit(%d, 100) = %d, want %d", test.requested, got, test.want)
		}
	}
	for _, test := range []struct {
		metadata map[string]string
		want     int64
	}{
		{metadata: nil},
		{metadata: map[string]string{"a": "b"}, want: 2},
		{metadata: map[string]string{"a": "bc", "de": "f"}, want: 6},
	} {
		if got := metadataSize(test.metadata); got != test.want {
			t.Errorf("metadataSize(%v) = %d, want %d", test.metadata, got, test.want)
		}
	}
	for _, test := range []struct {
		limit int
		want  bool
	}{{limit: -1}, {limit: 0}, {limit: 1, want: true}} {
		if got := positiveListLimit(test.limit); got != test.want {
			t.Errorf("positiveListLimit(%d) = %t, want %t", test.limit, got, test.want)
		}
	}
	for _, test := range []struct {
		lifetime time.Duration
		want     bool
	}{
		{lifetime: -1},
		{lifetime: 0},
		{lifetime: 1, want: true},
		{lifetime: maximumTemporaryURLLifetime, want: true},
		{lifetime: maximumTemporaryURLLifetime + 1},
	} {
		if got := validTemporaryURLLifetime(test.lifetime); got != test.want {
			t.Errorf("validTemporaryURLLifetime(%s) = %t, want %t", test.lifetime, got, test.want)
		}
	}
	if pageSize(100, 0) != 100 || pageSize(1001, 0) != 1000 || pageSize(1001, 1000) != 1 {
		t.Fatal("pageSize() boundary calculation is wrong")
	}
	token := "next"
	for _, test := range []struct {
		truncated bool
		token     *string
		complete  bool
	}{
		{complete: true},
		{token: &token, complete: true},
		{truncated: true, complete: true},
		{truncated: true, token: &token},
	} {
		output := &awss3.ListObjectsV2Output{IsTruncated: aws.Bool(test.truncated), NextContinuationToken: test.token}
		if got := paginationComplete(output); got != test.complete {
			t.Errorf("paginationComplete(%t, %v) = %t, want %t", test.truncated, test.token, got, test.complete)
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
	exact := map[string]string{"key": "value"}
	clone, err := adapter.cloneMetadata(exact)
	if err != nil || clone["key"] != "value" {
		t.Fatalf("cloneMetadata(exact limits) = %v, %v", clone, err)
	}
}

func TestListCommonPrefixLimitAndIteratorCurrentEntry(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend()
	backend.listOutputs = []*awss3.ListObjectsV2Output{{
		CommonPrefixes: []awstypes.CommonPrefix{
			{Prefix: aws.String("z/")},
			{Prefix: aws.String("a/")},
		},
	}}
	adapter := mustAdapter(t, backend, config{adapterName: "s3", bucket: "bucket", maxList: 1})
	iterator, err := adapter.List(context.Background(), filesystem.Root(), filesystem.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !iterator.Next() || iterator.Entry().Path.String() != "z" || iterator.Next() {
		t.Fatalf("limited common-prefix iterator entry = %+v", iterator.Entry())
	}
	if iterator.Entry().Path.String() != "z" {
		t.Fatal("iterator lost current entry after exhaustion")
	}
	if err := iterator.Close(); err != nil || iterator.Next() {
		t.Fatalf("Close() = %v", err)
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
