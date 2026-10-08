package r2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
)

type profileStream struct {
	reader *strings.Reader
	closed int
}

func (body *profileStream) Read(buffer []byte) (int, error) {
	if body.closed != 0 {
		return 0, errors.New("fixture stream is closed")
	}
	return body.reader.Read(buffer)
}

func (body *profileStream) Close() error {
	body.closed++
	return nil
}

type profileHTTPClient struct {
	body    *profileStream
	request *http.Request
	calls   int
}

func (client *profileHTTPClient) Do(request *http.Request) (*http.Response, error) {
	client.request = request
	client.calls++
	return &http.Response{
		StatusCode: http.StatusOK, Body: client.body, Request: request,
		Header: http.Header{"Content-Length": []string{"4"}}, ContentLength: 4,
	}, nil
}

func TestLoadPreservesSDKStreamingOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := &profileStream{reader: strings.NewReader("data")}
	transport := &profileHTTPClient{body: body}
	adapter, err := Load(ctx, Config{
		AccountID: "0123456789abcdef0123456789abcdef", Bucket: "bucket",
		AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret", Prefix: "tenant",
	}, WithHTTPClient(transport), WithEndpoint("https://fixture.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := adapter.Open(ctx, filesystem.MustParsePath("object"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if body.closed == 0 {
			_ = stream.Close()
		}
	}()
	if transport.calls != 1 || transport.request.URL.Path != "/bucket/tenant/object" {
		t.Fatal("R2 did not request the path-style prefixed object")
	}
	if !strings.Contains(transport.request.Header.Get("Authorization"), "/auto/s3/aws4_request") {
		t.Fatal("R2 did not sign with its auto region")
	}
	if body.closed != 0 {
		t.Fatal("R2 closed the successful stream before caller ownership")
	}
	contents, err := io.ReadAll(stream)
	if err != nil || string(contents) != "data" {
		t.Fatalf("stream = %q, error = %v", contents, err)
	}
	if err := stream.Close(); err != nil || body.closed != 1 {
		t.Fatalf("caller close error = %v, close count = %d", err, body.closed)
	}
}
