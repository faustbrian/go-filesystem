// Package s3 provides an Amazon S3 adapter backed by AWS SDK for Go v2.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/internal/redact"
	"github.com/faustbrian/golib/pkg/filesystem/internal/streamwriter"
)

const (
	maximumTemporaryURLLifetime = 7 * 24 * time.Hour
	defaultMaxMetadataEntries   = 128
	defaultMaxMetadataBytes     = int64(64 * 1024)
)

type objectClient interface {
	GetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	CopyObject(context.Context, *awss3.CopyObjectInput, ...func(*awss3.Options)) (*awss3.CopyObjectOutput, error)
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

type uploadClient interface {
	UploadObject(context.Context, *transfermanager.UploadObjectInput, ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error)
}

type presignClient interface {
	PresignGetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// Option configures an Adapter.
type Option func(*config) error

type config struct {
	adapterName        string
	bucket             string
	prefix             string
	maxList            int
	maxMetadataEntries int
	maxMetadataBytes   int64
	uploadOptions      []func(*transfermanager.Options)
}

// WithMetadataLimits bounds user metadata accepted from callers and returned
// by the service. Defaults are 128 entries and 64 KiB of keys plus values.
func WithMetadataLimits(maxEntries int, maxBytes int64) Option {
	return func(configuration *config) error {
		if maxEntries <= 0 || maxBytes <= 0 {
			return errors.New("s3: metadata limits must be positive")
		}
		configuration.maxMetadataEntries = maxEntries
		configuration.maxMetadataBytes = maxBytes
		return nil
	}
}

// WithPrefix places every logical path beneath prefix in the bucket.
func WithPrefix(prefix string) Option {
	return func(configuration *config) error {
		configuration.prefix = prefix
		return nil
	}
}

// WithMaxListEntries bounds one List call. The default is 10,000 entries.
func WithMaxListEntries(limit int) Option {
	return func(configuration *config) error {
		if limit <= 0 {
			return errors.New("s3: maximum list entries must be positive")
		}
		configuration.maxList = limit
		return nil
	}
}

// WithTransferOptions configures multipart threshold, part size, concurrency,
// cleanup timeout, and other AWS transfer-manager behavior.
func WithTransferOptions(options ...func(*transfermanager.Options)) Option {
	return func(configuration *config) error {
		configuration.uploadOptions = append(configuration.uploadOptions, options...)
		return nil
	}
}

// Adapter stores objects in one S3 bucket.
type Adapter struct {
	client             objectClient
	uploader           uploadClient
	presigner          presignClient
	adapterName        string
	bucket             string
	prefix             string
	maxList            int
	maxMetadataEntries int
	maxMetadataBytes   int64
	uploadOptions      []func(*transfermanager.Options)
	capabilities       filesystem.CapabilitySet
}

// New constructs an Amazon S3 adapter from an AWS SDK v2 client.
func New(client *awss3.Client, bucket string, options ...Option) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("s3: client is required")
	}
	configuration := config{
		adapterName:        "s3",
		bucket:             bucket,
		maxList:            10_000,
		maxMetadataEntries: defaultMaxMetadataEntries,
		maxMetadataBytes:   defaultMaxMetadataBytes,
	}
	for _, option := range options {
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	uploader := transfermanager.New(client, configuration.uploadOptions...)
	return newAdapter(client, uploader, awss3.NewPresignClient(client), configuration)
}

// NewR2Transport constructs the shared S3 transport with R2 semantics. Most
// callers should use package r2, which validates the R2 endpoint and region.
func NewR2Transport(client *awss3.Client, bucket string, options ...Option) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("r2: client is required")
	}
	configuration := config{
		adapterName:        "r2",
		bucket:             bucket,
		maxList:            10_000,
		maxMetadataEntries: defaultMaxMetadataEntries,
		maxMetadataBytes:   defaultMaxMetadataBytes,
	}
	for _, option := range options {
		if err := option(&configuration); err != nil {
			return nil, err
		}
	}
	uploader := transfermanager.New(client, configuration.uploadOptions...)
	return newAdapter(client, uploader, awss3.NewPresignClient(client), configuration)
}

func newAdapter(client objectClient, uploader uploadClient, presigner presignClient, configuration config) (*Adapter, error) {
	if client == nil || uploader == nil || presigner == nil {
		return nil, errors.New("s3: object, upload, and presign clients are required")
	}
	if strings.TrimSpace(configuration.bucket) == "" {
		return nil, errors.New("s3: bucket is required")
	}
	if configuration.maxList <= 0 {
		return nil, errors.New("s3: maximum list entries must be positive")
	}
	if configuration.maxMetadataEntries == 0 && configuration.maxMetadataBytes == 0 {
		configuration.maxMetadataEntries = defaultMaxMetadataEntries
		configuration.maxMetadataBytes = defaultMaxMetadataBytes
	}
	if configuration.maxMetadataEntries <= 0 || configuration.maxMetadataBytes <= 0 {
		return nil, errors.New("s3: metadata limits must be positive")
	}
	if configuration.adapterName != "s3" && configuration.adapterName != "r2" {
		return nil, fmt.Errorf("s3: invalid adapter profile %q", configuration.adapterName)
	}
	if configuration.prefix != "" {
		prefix, err := filesystem.ParsePath(configuration.prefix)
		if err != nil {
			return nil, fmt.Errorf("s3: invalid prefix: %w", err)
		}
		configuration.prefix = prefix.String()
	}

	return &Adapter{
		client:             client,
		uploader:           uploader,
		presigner:          presigner,
		adapterName:        configuration.adapterName,
		bucket:             configuration.bucket,
		prefix:             configuration.prefix,
		maxList:            configuration.maxList,
		maxMetadataEntries: configuration.maxMetadataEntries,
		maxMetadataBytes:   configuration.maxMetadataBytes,
		uploadOptions:      append([]func(*transfermanager.Options){}, configuration.uploadOptions...),
		capabilities: filesystem.NewCapabilitySet(
			filesystem.CapabilityRead,
			filesystem.CapabilityWrite,
			filesystem.CapabilityStreamingWrite,
			filesystem.CapabilityDelete,
			filesystem.CapabilityList,
			filesystem.CapabilityStat,
			filesystem.CapabilityCopy,
			filesystem.CapabilityRangeRead,
			filesystem.CapabilityMetadata,
			filesystem.CapabilityTemporaryURL,
			filesystem.CapabilityMultipart,
		),
	}, nil
}

// Capabilities reports explicitly supported S3 operations. Move is omitted
// because S3 has no atomic rename primitive.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return a.capabilities
}

// Open returns the SDK response body directly as a streaming reader.
func (a *Adapter) Open(ctx context.Context, logicalPath filesystem.Path) (io.ReadCloser, error) {
	output, err := a.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(logicalPath)),
	})
	if err != nil {
		return nil, mapError(logicalPath, err)
	}
	return output.Body, nil
}

// OpenRange returns an inclusive HTTP byte range as a stream.
func (a *Adapter) OpenRange(ctx context.Context, logicalPath filesystem.Path, byteRange filesystem.ByteRange) (io.ReadCloser, error) {
	if byteRange.Offset < 0 || byteRange.Length <= 0 || byteRange.Offset > byteRange.Offset+byteRange.Length-1 {
		return nil, fmt.Errorf("%w: offset=%d length=%d", filesystem.ErrInvalidRange, byteRange.Offset, byteRange.Length)
	}
	end := byteRange.Offset + byteRange.Length - 1
	output, err := a.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(logicalPath)),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", byteRange.Offset, end)),
	})
	if err != nil {
		return nil, mapError(logicalPath, err)
	}
	return output.Body, nil
}

// Write streams source through the AWS transfer manager, which selects a
// bounded single-part or multipart upload and aborts failed multipart uploads.
func (a *Adapter) Write(ctx context.Context, logicalPath filesystem.Path, source io.Reader, options filesystem.WriteOptions) (filesystem.Metadata, error) {
	if logicalPath.IsRoot() {
		return filesystem.Metadata{}, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	metadata, err := a.cloneMetadata(options.Metadata)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	input := &transfermanager.UploadObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(a.key(logicalPath)),
		Body:        source,
		ContentType: optionalString(options.ContentType),
		Metadata:    metadata,
	}
	if options.IfNoneMatch {
		input.IfNoneMatch = aws.String("*")
	}
	if _, err := a.uploader.UploadObject(ctx, input, a.uploadOptions...); err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	if err := ctx.Err(); err != nil {
		return filesystem.Metadata{}, err
	}
	return a.Stat(ctx, logicalPath)
}

// OpenWriter returns a streaming writer that completes its SDK upload on
// Close and reports upload or multipart-cleanup failures.
func (a *Adapter) OpenWriter(ctx context.Context, logicalPath filesystem.Path, options filesystem.WriteOptions) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if logicalPath.IsRoot() {
		return nil, fmt.Errorf("%w: object path is root", filesystem.ErrInvalidPath)
	}
	return streamwriter.New(func(source io.Reader) error {
		_, err := a.Write(ctx, logicalPath, source, options)
		return err
	}), nil
}

// Delete removes an object.
func (a *Adapter) Delete(ctx context.Context, logicalPath filesystem.Path) error {
	_, err := a.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(logicalPath)),
	})
	return mapError(logicalPath, err)
}

// Stat retrieves object metadata.
func (a *Adapter) Stat(ctx context.Context, logicalPath filesystem.Path) (filesystem.Metadata, error) {
	output, err := a.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(logicalPath)),
	})
	if err != nil {
		return filesystem.Metadata{}, mapError(logicalPath, err)
	}
	metadata, err := a.cloneMetadata(output.Metadata)
	if err != nil {
		return filesystem.Metadata{}, err
	}
	return filesystem.Metadata{
		Path:         logicalPath,
		Kind:         filesystem.EntryKindFile,
		Size:         aws.ToInt64(output.ContentLength),
		Modified:     aws.ToTime(output.LastModified),
		ETag:         strings.Trim(aws.ToString(output.ETag), `"`),
		ContentType:  aws.ToString(output.ContentType),
		UserMetadata: metadata,
	}, nil
}

// List retrieves a bounded, deterministic object snapshot.
func (a *Adapter) List(ctx context.Context, directory filesystem.Path, options filesystem.ListOptions) (filesystem.EntryIterator, error) {
	if options.Limit < 0 {
		return nil, errors.New("s3: list limit must not be negative")
	}
	limit := options.Limit
	if limit == 0 || limit > a.maxList {
		limit = a.maxList
	}
	prefix := a.prefix
	if !directory.IsRoot() {
		prefix = a.key(directory)
	}
	if prefix != "" {
		prefix += "/"
	}
	input := &awss3.ListObjectsV2Input{
		Bucket:  aws.String(a.bucket),
		Prefix:  optionalString(prefix),
		MaxKeys: aws.Int32(int32(min(limit, 1000))),
	}
	if !options.Recursive {
		input.Delimiter = aws.String("/")
	}

	entries := make([]filesystem.Entry, 0, limit)
	for len(entries) < limit {
		output, err := a.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, mapError(directory, err)
		}
		for _, object := range output.Contents {
			logicalPath, ok := a.logicalPath(aws.ToString(object.Key), false)
			if !ok {
				continue
			}
			entries = append(entries, filesystem.Entry{
				Path:     logicalPath,
				Kind:     filesystem.EntryKindFile,
				Size:     aws.ToInt64(object.Size),
				Modified: aws.ToTime(object.LastModified),
			})
			if len(entries) == limit {
				break
			}
		}
		for _, commonPrefix := range output.CommonPrefixes {
			if len(entries) == limit {
				break
			}
			logicalPath, ok := a.logicalPath(aws.ToString(commonPrefix.Prefix), true)
			if ok {
				entries = append(entries, filesystem.Entry{Path: logicalPath, Kind: filesystem.EntryKindDirectory})
			}
		}
		if !aws.ToBool(output.IsTruncated) || output.NextContinuationToken == nil {
			break
		}
		input.ContinuationToken = output.NextContinuationToken
		input.MaxKeys = aws.Int32(int32(min(limit-len(entries), 1000)))
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Path.String() < entries[right].Path.String()
	})
	return &iterator{entries: entries, index: -1}, nil
}

// Copy performs a server-side copy. No-clobber copy is rejected because S3
// lacks a destination-side conditional CopyObject primitive.
func (a *Adapter) Copy(ctx context.Context, source, destination filesystem.Path, options filesystem.CopyOptions) error {
	if !options.Overwrite {
		return filesystem.Unsupported(a.adapterName, filesystem.CapabilityCopy, filesystem.OperationCopy)
	}
	copySource := url.PathEscape(a.bucket + "/" + a.key(source))
	_, err := a.client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:     aws.String(a.bucket),
		Key:        aws.String(a.key(destination)),
		CopySource: aws.String(copySource),
	})
	return mapError(source, err)
}

// Move is unsupported because copy-and-delete is not an atomic rename.
func (a *Adapter) Move(context.Context, filesystem.Path, filesystem.Path, filesystem.MoveOptions) error {
	return filesystem.Unsupported(a.adapterName, filesystem.CapabilityMove, filesystem.OperationMove)
}

// SetMetadata replaces object metadata with a server-side self-copy.
func (a *Adapter) SetMetadata(ctx context.Context, logicalPath filesystem.Path, metadata map[string]string) error {
	bounded, err := a.cloneMetadata(metadata)
	if err != nil {
		return err
	}
	current, err := a.Stat(ctx, logicalPath)
	if err != nil {
		return err
	}
	key := a.key(logicalPath)
	_, err = a.client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:            aws.String(a.bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(url.PathEscape(a.bucket + "/" + key)),
		Metadata:          bounded,
		MetadataDirective: awstypes.MetadataDirectiveReplace,
		ContentType:       optionalString(current.ContentType),
	})
	return mapError(logicalPath, err)
}

// Checksum returns a typed unsupported error because ETags and multipart
// checksums do not provide one portable algorithm guarantee.
func (a *Adapter) Checksum(context.Context, filesystem.Path, filesystem.ChecksumAlgorithm) (filesystem.Checksum, error) {
	return filesystem.Checksum{}, filesystem.Unsupported(a.adapterName, filesystem.CapabilityChecksum, filesystem.OperationChecksum)
}

// TemporaryURL creates a presigned GetObject URL valid for at most seven days.
func (a *Adapter) TemporaryURL(ctx context.Context, logicalPath filesystem.Path, lifetime time.Duration, options filesystem.TemporaryURLOptions) (string, error) {
	if lifetime <= 0 || lifetime > maximumTemporaryURLLifetime {
		return "", fmt.Errorf("s3: temporary URL lifetime must be between 1ns and %s", maximumTemporaryURLLifetime)
	}
	input := &awss3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(logicalPath)),
	}
	if options.DownloadName != "" {
		if containsControl(options.DownloadName) {
			return "", errors.New("s3: download name contains a control character")
		}
		input.ResponseContentDisposition = aws.String(mime.FormatMediaType("attachment", map[string]string{"filename": options.DownloadName}))
	}
	input.ResponseContentType = optionalString(options.ContentType)
	request, err := a.presigner.PresignGetObject(ctx, input, func(configuration *awss3.PresignOptions) {
		configuration.Expires = lifetime
	})
	if err != nil {
		return "", mapError(logicalPath, err)
	}
	return request.URL, nil
}

// Visibility returns a typed unsupported error. S3 ACL support depends on
// bucket ownership settings and is not claimed by the default profile.
func (a *Adapter) Visibility(context.Context, filesystem.Path) (filesystem.Visibility, error) {
	return "", filesystem.Unsupported(a.adapterName, filesystem.CapabilityVisibility, filesystem.OperationVisibility)
}

// SetVisibility returns a typed unsupported error for the default profile.
func (a *Adapter) SetVisibility(context.Context, filesystem.Path, filesystem.Visibility) error {
	return filesystem.Unsupported(a.adapterName, filesystem.CapabilityVisibility, filesystem.OperationSetVisibility)
}

func (a *Adapter) key(logicalPath filesystem.Path) string {
	if a.prefix == "" {
		return logicalPath.String()
	}
	return a.prefix + "/" + logicalPath.String()
}

func (a *Adapter) logicalPath(key string, directory bool) (filesystem.Path, bool) {
	if directory {
		key = strings.TrimSuffix(key, "/")
	}
	if a.prefix != "" {
		prefix := a.prefix + "/"
		if !strings.HasPrefix(key, prefix) {
			return filesystem.Path{}, false
		}
		key = strings.TrimPrefix(key, prefix)
	}
	logicalPath, err := filesystem.ParsePath(key)
	return logicalPath, err == nil
}

type iterator struct {
	entries []filesystem.Entry
	index   int
	closed  bool
}

func (i *iterator) Next() bool {
	if i.closed || i.index+1 >= len(i.entries) {
		return false
	}
	i.index++
	return true
}

func (i *iterator) Entry() filesystem.Entry {
	if i.index < 0 || i.index >= len(i.entries) {
		return filesystem.Entry{}
	}
	return i.entries[i.index]
}

func (i *iterator) Err() error { return nil }

func (i *iterator) Close() error {
	i.closed = true
	return nil
}

func mapError(logicalPath filesystem.Path, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return fmt.Errorf("%w: %s", filesystem.ErrNotFound, logicalPath)
		case "PreconditionFailed":
			return fmt.Errorf("%w: %s", filesystem.ErrPreconditionFailed, logicalPath)
		case "InvalidRange", "RequestedRangeNotSatisfiable":
			return fmt.Errorf("%w: %s", filesystem.ErrInvalidRange, logicalPath)
		}
	}
	return redact.Error(err)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

func (a *Adapter) cloneMetadata(source map[string]string) (map[string]string, error) {
	if source == nil {
		return nil, nil
	}
	if len(source) > a.maxMetadataEntries {
		return nil, fmt.Errorf(
			"%w: %s metadata has %d entries, maximum %d",
			filesystem.ErrResourceLimit,
			a.adapterName,
			len(source),
			a.maxMetadataEntries,
		)
	}
	var size int64
	for key, value := range source {
		remaining := a.maxMetadataBytes - size
		entrySize := int64(len(key)) + int64(len(value))
		if entrySize > remaining {
			return nil, fmt.Errorf(
				"%w: %s metadata exceeds %d bytes",
				filesystem.ErrResourceLimit,
				a.adapterName,
				a.maxMetadataBytes,
			)
		}
		size += entrySize
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
