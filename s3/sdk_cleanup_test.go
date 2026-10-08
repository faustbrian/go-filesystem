package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	filesystem "github.com/faustbrian/go-filesystem/v2"
)

var errSDKInterceptor = errors.New("fixture response interceptor rejection")

type sdkResponseBody struct {
	reader *strings.Reader
	closed int
}

func (body *sdkResponseBody) Read(buffer []byte) (int, error) {
	if body.closed != 0 {
		return 0, errors.New("fixture response is closed")
	}
	return body.reader.Read(buffer)
}

func (body *sdkResponseBody) Close() error {
	body.closed++
	return nil
}

type sdkHTTPClient struct {
	body    *sdkResponseBody
	err     error
	calls   int
	request *http.Request
}

func (client *sdkHTTPClient) Do(request *http.Request) (*http.Response, error) {
	client.calls++
	client.request = request
	if client.err != nil {
		return nil, client.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Length": []string{"4"},
			"Content-Type":   []string{"application/octet-stream"},
			"Etag":           []string{`"fixture-etag"`},
		},
		Body: client.body, ContentLength: 4, Request: request,
	}, nil
}

type sdkRejectResponse struct{}

func (sdkRejectResponse) AfterTransmit(context.Context, *smithyhttp.InterceptorContext) error {
	return errSDKInterceptor
}

func (sdkRejectResponse) BeforeDeserialization(context.Context, *smithyhttp.InterceptorContext) error {
	return errSDKInterceptor
}

// Exercise the real generated SDK middleware, not the adapter's fake backend.
func TestSDKResponseOwnership(t *testing.T) {
	for _, operation := range []string{"open", "range", "stat"} {
		for _, stage := range []string{"success", "after-transmit", "before-deserialization", "transport-error"} {
			t.Run(operation+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				body := &sdkResponseBody{reader: strings.NewReader("data")}
				transport := &sdkHTTPClient{body: body}
				transportError := errors.New("fixture transport failure")
				options := awss3.Options{
					Region: "us-east-1", Credentials: aws.AnonymousCredentials{},
					BaseEndpoint: aws.String("https://fixture.invalid"),
					HTTPClient:   transport, RetryMaxAttempts: 1, UsePathStyle: true,
				}
				switch stage {
				case "after-transmit":
					options.Interceptors.AddAfterTransmit(sdkRejectResponse{})
				case "before-deserialization":
					options.Interceptors.AddBeforeDeserialization(sdkRejectResponse{})
				case "transport-error":
					transport.err = transportError
				}
				adapter, err := New(awss3.New(options), "bucket", WithPrefix("tenant"))
				if err != nil {
					t.Fatal(err)
				}
				path := filesystem.MustParsePath("object")
				var stream io.ReadCloser
				switch operation {
				case "open":
					stream, err = adapter.Open(ctx, path)
				case "range":
					stream, err = adapter.OpenRange(ctx, path, filesystem.ByteRange{Offset: 2, Length: 4})
				case "stat":
					var metadata filesystem.Metadata
					metadata, err = adapter.Stat(ctx, path)
					if stage == "success" && (metadata.Size != 4 || metadata.ETag != "fixture-etag") {
						t.Fatalf("unexpected metadata: %+v", metadata)
					}
				}
				if transport.calls != 1 || transport.request.URL.Path != "/bucket/tenant/object" {
					t.Fatal("operation did not make one request to the prefixed object")
				}
				if operation == "range" && transport.request.Header.Get("Range") != "bytes=2-5" {
					t.Fatal("operation changed the inclusive range")
				}
				if stage != "success" {
					expected := errSDKInterceptor
					if stage == "transport-error" {
						expected = transportError
					}
					if !errors.Is(err, expected) || stream != nil {
						t.Fatalf("expected original error and no stream, got %v", err)
					}
					if stage != "transport-error" && body.closed == 0 {
						t.Fatal("received response body was not closed after interceptor rejection")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if operation == "stat" {
					if body.closed == 0 {
						t.Fatal("nonstreaming success did not close its response")
					}
					return
				}
				defer func() {
					if body.closed == 0 {
						_ = stream.Close()
					}
				}()
				if body.closed != 0 {
					t.Fatal("successful stream was closed before ownership transfer")
				}
				contents, readErr := io.ReadAll(stream)
				if readErr != nil || string(contents) != "data" {
					t.Fatalf("stream = %q, error = %v", contents, readErr)
				}
				if err := stream.Close(); err != nil || body.closed != 1 {
					t.Fatalf("caller close error = %v, close count = %d", err, body.closed)
				}
			})
		}
	}
}
