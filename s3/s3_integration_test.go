package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	filesystemS3 "github.com/faustbrian/golib/pkg/filesystem/s3"
)

func TestCompatibleServiceConformance(t *testing.T) {
	endpoint := os.Getenv("S3_INTEGRATION_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_INTEGRATION_ENDPOINT is not set")
	}
	accessKey := os.Getenv("S3_INTEGRATION_ACCESS_KEY")
	secretKey := os.Getenv("S3_INTEGRATION_SECRET_KEY")
	client := awss3.New(awss3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
	})
	bucket := fmt.Sprintf("filesystem-%d", time.Now().UnixNano())
	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		paginator := awss3.NewListObjectsV2Paginator(client, &awss3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				t.Errorf("list cleanup objects: %v", err)
				break
			}
			for _, object := range page.Contents {
				if _, err := client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(bucket), Key: object.Key}); err != nil {
					t.Errorf("delete cleanup object: %v", err)
				}
			}
		}
		if _, err := client.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Errorf("delete cleanup bucket: %v", err)
		}
	})
	var sequence atomic.Uint64
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		adapter, err := filesystemS3.New(
			client,
			bucket,
			filesystemS3.WithPrefix(fmt.Sprintf("case-%d", sequence.Add(1))),
			filesystemS3.WithMaxListEntries(100),
		)
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
	t.Run("multipart cleanup", func(t *testing.T) {
		testMultipartLifecycle(t, client, bucket, "s3-multipart")
	})
}

func testMultipartLifecycle(t *testing.T, client *awss3.Client, bucket, prefix string) {
	t.Helper()
	const partSize = int64(5 * 1024 * 1024)
	adapter, err := filesystemS3.New(
		client,
		bucket,
		filesystemS3.WithPrefix(prefix),
		filesystemS3.WithTransferOptions(func(options *transfermanager.Options) {
			options.PartSizeBytes = partSize
			options.MultipartUploadThreshold = partSize
			options.Concurrency = 1
			options.FailTimeout = 5 * time.Second
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	success := filesystem.MustParsePath("success.bin")
	metadata, err := adapter.Write(
		context.Background(),
		success,
		io.LimitReader(zeroReader{}, 2*partSize+1),
		filesystem.WriteOptions{},
	)
	if err != nil || metadata.Size != 2*partSize+1 {
		t.Fatalf("multipart Write() = %+v, %v", metadata, err)
	}

	injected := errors.New("injected disconnect")
	failed := filesystem.MustParsePath("failed.bin")
	reader := fstest.NewFaultReader(
		io.LimitReader(zeroReader{}, 2*partSize),
		fstest.FaultReaderOptions{FailAfter: partSize + 1, Err: injected},
	)
	if _, err := adapter.Write(context.Background(), failed, reader, filesystem.WriteOptions{}); !errors.Is(err, injected) {
		t.Fatalf("multipart Write(fault) error = %v", err)
	}
	uploads, err := client.ListMultipartUploads(context.Background(), &awss3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix + "/"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(uploads.Uploads) != 0 {
		t.Fatalf("orphaned multipart uploads = %d", len(uploads.Uploads))
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
