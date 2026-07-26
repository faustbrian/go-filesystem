package r2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
)

var validConfig = Config{
	AccountID:       "0123456789abcdef0123456789abcdef",
	Bucket:          "bucket",
	AccessKeyID:     "access-key",
	SecretAccessKey: "secret-key",
}

func TestOptionsReachR2Transport(t *testing.T) {
	t.Parallel()

	httpClient := &stubHTTPClient{}
	transferOption := func(options *transfermanager.Options) {
		options.PartSizeBytes = 8 * 1024 * 1024
	}
	configuration := validConfig
	configuration.Prefix = "tenant//objects"
	adapter, err := New(
		context.Background(),
		configuration,
		WithEndpoint("https://r2.example.test/"),
		WithMaxListEntries(25),
		WithMetadataLimits(16, 4*1024),
		WithTransferOptions(transferOption),
		WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Profile().Endpoint != "https://r2.example.test" {
		t.Fatalf("endpoint = %q", adapter.Profile().Endpoint)
	}
	if adapter.client.Options().HTTPClient != httpClient {
		t.Fatal("custom HTTP client was not retained")
	}

	configuration.Prefix = "../escape"
	if _, err := New(context.Background(), configuration); err == nil {
		t.Fatal("New(invalid prefix) error = nil")
	}
	if _, err := New(context.Background(), validConfig, WithMaxListEntries(0)); err == nil {
		t.Fatal("New(invalid maximum) error = nil")
	}
	if _, err := New(context.Background(), validConfig, WithMetadataLimits(0, 1)); err == nil {
		t.Fatal("New(invalid metadata entries) error = nil")
	}
	if _, err := New(context.Background(), validConfig, WithMetadataLimits(1, 0)); err == nil {
		t.Fatal("New(invalid metadata bytes) error = nil")
	}
}

func TestEndpointValidationMatrix(t *testing.T) {
	t.Parallel()

	valid := []struct {
		raw         string
		development bool
		want        string
	}{
		{raw: "https://r2.example.test/", want: "https://r2.example.test"},
		{raw: "http://localhost:9000/", development: true, want: "http://localhost:9000"},
		{raw: "http://[::1]:9000", development: true, want: "http://[::1]:9000"},
		{raw: "https://127.0.0.1", development: true, want: "https://127.0.0.1"},
	}
	for _, test := range valid {
		got, err := validateEndpoint(test.raw, test.development)
		if err != nil || got != test.want {
			t.Fatalf("validateEndpoint(%q) = %q, %v", test.raw, got, err)
		}
	}
	for _, test := range []struct {
		raw         string
		development bool
	}{
		{raw: "%"},
		{raw: "https:///missing-host"},
		{raw: "https://r2.example.test#fragment"},
		{raw: "ftp://localhost", development: true},
		{raw: "http://example.test", development: true},
	} {
		if _, err := validateEndpoint(test.raw, test.development); err == nil {
			t.Fatalf("validateEndpoint(%q) error = nil", test.raw)
		}
	}
	if !isLoopback("LOCALHOST") || !isLoopback("127.0.0.1") || isLoopback("not-an-address") || isLoopback("192.0.2.1") {
		t.Fatal("isLoopback() classification is wrong")
	}
}

func TestEndpointOptionOrderingDoesNotPermitDowngrade(t *testing.T) {
	t.Parallel()

	_, err := New(
		context.Background(),
		validConfig,
		WithDevelopmentEndpoint("http://localhost:9000"),
		WithEndpoint("http://localhost:9000"),
	)
	if err == nil {
		t.Fatal("WithEndpoint re-enabled an HTTP development endpoint")
	}
}

func TestConfigurationLoaderFailureIsWrapped(t *testing.T) {
	t.Parallel()

	injected := fmt.Errorf(
		"configuration unavailable for %s with %s",
		validConfig.AccessKeyID,
		validConfig.SecretAccessKey,
	)
	_, err := newWithLoader(
		context.Background(),
		validConfig,
		func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
			return aws.Config{}, injected
		},
	)
	if !errors.Is(err, injected) {
		t.Fatalf("newWithLoader() error = %v", err)
	}
	if strings.Contains(err.Error(), validConfig.AccessKeyID) || strings.Contains(err.Error(), validConfig.SecretAccessKey) {
		t.Fatalf("newWithLoader() error leaked credentials: %v", err)
	}
}

type stubHTTPClient struct{}

func (*stubHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected HTTP request")
}
