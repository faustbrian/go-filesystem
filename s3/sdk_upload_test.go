package s3_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	filesystem "github.com/faustbrian/go-filesystem/v2"
	"github.com/faustbrian/go-filesystem/v2/fstest"
	"github.com/faustbrian/go-filesystem/v2/r2"
	filesystemS3 "github.com/faustbrian/go-filesystem/v2/s3"
)

const uploadPartSize = 5 * 1024 * 1024

// This fixture is an HTTP boundary, not a fake upload backend: the real SDK
// and transfer manager own serialization, part selection, and abort handling.
type uploadHTTPFixture struct {
	mu                                             sync.Mutex
	object                                         []byte
	parts                                          map[int][]byte
	active                                         bool
	initiated, completed, aborted, heads           int
	failPart                                       int
	precondition                                   bool
	contentType, metadata, condition, signingScope string
	explicitCredential                             bool
}

func (f *uploadHTTPFixture) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.URL.Path != "/bucket/tenant/object" {
		return nil, errors.New("unexpected object path")
	}
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	reader := io.Reader(http.NoBody)
	if req.Body != nil {
		reader = req.Body
		if strings.Contains(req.Header.Get("Content-Encoding"), "aws-chunked") {
			reader = httputil.NewChunkedReader(reader)
		}
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	status, body := http.StatusOK, ""
	header := http.Header{"Content-Type": []string{"application/xml"}, "Etag": []string{`"object-etag"`}}
	query := req.URL.Query()
	switch {
	case req.Method == http.MethodPost && query.Has("uploads"):
		f.active = true
		f.initiated++
		f.parts = make(map[int][]byte)
		f.contentType = req.Header.Get("Content-Type")
		f.metadata = req.Header.Get("X-Amz-Meta-Owner")
		body = `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>tenant/object</Key><UploadId>fixture-upload</UploadId></InitiateMultipartUploadResult>`
	case req.Method == http.MethodPut && query.Get("uploadId") != "":
		if !f.active || query.Get("uploadId") != "fixture-upload" {
			return nil, errors.New("part targets no active upload")
		}
		part, parseErr := strconv.Atoi(query.Get("partNumber"))
		if parseErr != nil {
			return nil, parseErr
		}
		if part == f.failPart {
			status = http.StatusServiceUnavailable
			body = `<Error><Code>SlowDown</Code><Message>fixture part rejected</Message></Error>`
		} else {
			f.parts[part] = data
			header.Set("Etag", fmt.Sprintf(`"part-%d"`, part))
		}
	case req.Method == http.MethodPost && query.Get("uploadId") == "fixture-upload":
		var completion struct {
			Parts []struct {
				Number int    `xml:"PartNumber"`
				ETag   string `xml:"ETag"`
			} `xml:"Part"`
		}
		if err := xml.Unmarshal(data, &completion); err != nil {
			return nil, err
		}
		for index, part := range completion.Parts {
			if part.Number != index+1 || part.ETag != fmt.Sprintf(`"part-%d"`, part.Number) || f.parts[part.Number] == nil {
				return nil, errors.New("completion has missing or unordered parts")
			}
			f.object = append(f.object, f.parts[part.Number]...)
		}
		f.active = false
		f.completed++
		body = `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>tenant/object</Key><ETag>"object-etag"</ETag></CompleteMultipartUploadResult>`
	case req.Method == http.MethodDelete && query.Get("uploadId") == "fixture-upload":
		if !f.active {
			return nil, errors.New("abort targets no active upload")
		}
		f.active = false
		f.parts = nil
		f.aborted++
		status = http.StatusNoContent
	case req.Method == http.MethodPut && !query.Has("uploadId"):
		f.contentType = req.Header.Get("Content-Type")
		f.metadata = req.Header.Get("X-Amz-Meta-Owner")
		f.condition = req.Header.Get("If-None-Match")
		if strings.Contains(req.Header.Get("Authorization"), "/auto/s3/aws4_request") {
			f.signingScope = "auto/s3/aws4_request"
		}
		f.explicitCredential = strings.Contains(req.Header.Get("Authorization"), "Credential=fixture-access/")
		if f.precondition {
			status = http.StatusPreconditionFailed
			body = `<Error><Code>PreconditionFailed</Code><Message>fixture object exists</Message></Error>`
		} else {
			f.object = data
		}
	case req.Method == http.MethodHead:
		f.heads++
		header.Set("Content-Length", strconv.Itoa(len(f.object)))
		header.Set("Content-Type", f.contentType)
		header.Set("X-Amz-Meta-Owner", f.metadata)
	default:
		return nil, errors.New("unexpected SDK operation")
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func uploadAdapter(t *testing.T, fixture *uploadHTTPFixture) *filesystemS3.Adapter {
	t.Helper()
	client := awss3.New(awss3.Options{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}, BaseEndpoint: aws.String("https://fixture.invalid"), HTTPClient: fixture, RetryMaxAttempts: 1, UsePathStyle: true})
	adapter, err := filesystemS3.New(client, "bucket", filesystemS3.WithPrefix("tenant"), filesystemS3.WithTransferOptions(func(options *transfermanager.Options) {
		options.PartSizeBytes = uploadPartSize
		options.MultipartUploadThreshold = uploadPartSize
		options.Concurrency = 1
		options.FailTimeout = time.Second
	}))
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestSDKUploadSinglePartPropertiesAndPrecondition(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(strconv.FormatBool(reject), func(t *testing.T) {
			fixture := &uploadHTTPFixture{precondition: reject}
			adapter := uploadAdapter(t, fixture)
			payload := []byte("literal\x00payload: not a repeated byte")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filesystem.MustParsePath("object")
			metadata, err := adapter.Write(ctx, path, bytes.NewReader(payload), filesystem.WriteOptions{ContentType: "application/test", Metadata: map[string]string{"owner": "fixture"}, IfNoneMatch: true})
			if reject {
				if !errors.Is(err, filesystem.ErrPreconditionFailed) || fixture.heads != 0 || fixture.object != nil {
					t.Fatalf("rejected upload: err=%v heads=%d published=%v", err, fixture.heads, fixture.object != nil)
				}
			} else if err != nil || !bytes.Equal(fixture.object, payload) || metadata.Path != path || metadata.Size != int64(len(payload)) || metadata.ETag != "object-etag" || metadata.ContentType != "application/test" || metadata.UserMetadata["owner"] != "fixture" || fixture.heads != 1 {
				t.Fatalf("successful upload: metadata=%+v err=%v heads=%d", metadata, err, fixture.heads)
			}
			if fixture.condition != "*" || fixture.contentType != "application/test" || fixture.metadata != "fixture" {
				t.Fatal("upload dropped conditional write or object properties")
			}
		})
	}
}

func TestSDKUploadMultipartPublicationAndAbort(t *testing.T) {
	payload := make([]byte, 2*uploadPartSize+1)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	sourceFailure := errors.New("fixture source failed")
	for _, mode := range []string{"success", "source-error", "part-error", "writer-part-error"} {
		t.Run(mode, func(t *testing.T) {
			fixture := &uploadHTTPFixture{}
			if strings.Contains(mode, "part-error") {
				fixture.failPart = 2
			}
			adapter := uploadAdapter(t, fixture)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var source io.Reader = bytes.NewReader(payload)
			if mode == "source-error" {
				source = fstest.NewFaultReader(source, fstest.FaultReaderOptions{FailAfter: uploadPartSize + 1, Err: sourceFailure})
			}
			var metadata filesystem.Metadata
			var err error
			if mode == "writer-part-error" {
				writer, openErr := adapter.OpenWriter(ctx, filesystem.MustParsePath("object"), filesystem.WriteOptions{})
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, _ = writer.Write(payload)
				err = writer.Close()
				if err == nil || writer.Close() != err {
					t.Fatal("Close lost asynchronous upload failure or changed it on repetition")
				}
			} else {
				metadata, err = adapter.Write(ctx, filesystem.MustParsePath("object"), source, filesystem.WriteOptions{})
			}
			if mode == "success" {
				if err != nil || !bytes.Equal(fixture.object, payload) || metadata.Size != int64(len(payload)) || fixture.completed != 1 || fixture.aborted != 0 || len(fixture.parts) != 3 || len(fixture.parts[3]) != 1 {
					t.Fatalf("multipart publication: err=%v size=%d complete=%d abort=%d parts=%d", err, metadata.Size, fixture.completed, fixture.aborted, len(fixture.parts))
				}
			} else {
				if err == nil || fixture.object != nil || fixture.completed != 0 || fixture.aborted != 1 || fixture.heads != 0 {
					t.Fatalf("multipart failure: err=%v complete=%d abort=%d heads=%d published=%v", err, fixture.completed, fixture.aborted, fixture.heads, fixture.object != nil)
				}
				if mode == "source-error" && !errors.Is(err, sourceFailure) {
					t.Fatalf("lost original source failure: %v", err)
				}
			}
			if fixture.active || fixture.initiated != 1 {
				t.Fatalf("multipart lifecycle active=%v initiated=%d", fixture.active, fixture.initiated)
			}
		})
	}
}

func TestR2LoadUploadsWithExplicitCredentialProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "empty-profile")
	if err := os.WriteFile(profile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", profile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", profile)
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-invalid")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-invalid")
	t.Setenv("AWS_REGION", "ambient-invalid")
	fixture := &uploadHTTPFixture{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	adapter, err := r2.Load(ctx, r2.Config{AccountID: "0123456789abcdef0123456789abcdef", Bucket: "bucket", AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", Prefix: "tenant"}, r2.WithHTTPClient(fixture), r2.WithEndpoint("https://fixture.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("R2 literal upload")
	metadata, err := adapter.Write(ctx, filesystem.MustParsePath("object"), bytes.NewReader(payload), filesystem.WriteOptions{ContentType: "application/test", Metadata: map[string]string{"owner": "fixture"}})
	if err != nil || !bytes.Equal(fixture.object, payload) || metadata.Size != int64(len(payload)) || fixture.signingScope != "auto/s3/aws4_request" || !fixture.explicitCredential || fixture.metadata != "fixture" {
		t.Fatalf("R2 upload: err=%v size=%d scope=%q", err, metadata.Size, fixture.signingScope)
	}
}
