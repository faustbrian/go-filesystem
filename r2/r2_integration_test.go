package r2_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/faustbrian/go-filesystem/fstest"
	filesystemR2 "github.com/faustbrian/go-filesystem/r2"
)

func TestCompatibleServiceConformance(t *testing.T) {
	endpoint := os.Getenv("S3_INTEGRATION_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_INTEGRATION_ENDPOINT is not set")
	}
	accessKey := os.Getenv("S3_INTEGRATION_ACCESS_KEY")
	secretKey := os.Getenv("S3_INTEGRATION_SECRET_KEY")
	client := awss3.New(awss3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
	})
	bucket := fmt.Sprintf("go-filesystem-r2-%d", time.Now().UnixNano())
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
		adapter, err := filesystemR2.New(
			context.Background(),
			filesystemR2.Config{
				AccountID:       "0123456789abcdef0123456789abcdef",
				Bucket:          bucket,
				AccessKeyID:     accessKey,
				SecretAccessKey: secretKey,
				Prefix:          fmt.Sprintf("case-%d", sequence.Add(1)),
			},
			filesystemR2.WithDevelopmentEndpoint(endpoint),
			filesystemR2.WithMaxListEntries(100),
		)
		if err != nil {
			t.Fatal(err)
		}
		return adapter
	})
}
