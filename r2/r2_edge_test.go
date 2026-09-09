package r2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	filesystem "github.com/faustbrian/go-filesystem"
	filesystemS3 "github.com/faustbrian/go-filesystem/s3"
)

var errInjected = errors.New("injected R2 failure")

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
	if _, err := Load(context.Background(), configuration); !errors.Is(err, filesystem.ErrInvalidPath) || !strings.HasPrefix(err.Error(), "r2: invalid prefix:") {
		t.Fatalf("Load(invalid prefix) error = %v", err)
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

func TestLimitValidationBoundaries(t *testing.T) {
	t.Parallel()

	valid := settings{maxListEntries: 1, maxMetadataEntries: 1, maxMetadataBytes: 1}
	if err := validateLimits(valid); err != nil {
		t.Fatalf("validateLimits(valid) = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*settings)
	}{
		{name: "zero list", mutate: func(value *settings) { value.maxListEntries = 0 }},
		{name: "negative list", mutate: func(value *settings) { value.maxListEntries = -1 }},
		{name: "zero metadata entries", mutate: func(value *settings) { value.maxMetadataEntries = 0 }},
		{name: "negative metadata entries", mutate: func(value *settings) { value.maxMetadataEntries = -1 }},
		{name: "zero metadata bytes", mutate: func(value *settings) { value.maxMetadataBytes = 0 }},
		{name: "negative metadata bytes", mutate: func(value *settings) { value.maxMetadataBytes = -1 }},
	} {
		value := valid
		test.mutate(&value)
		if err := validateLimits(value); err == nil {
			t.Errorf("validateLimits(%s) error = nil", test.name)
		}
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

func TestTransportConstructionFailureIsPreserved(t *testing.T) {
	loads := 0
	_, err := newWithLoaderAndTransport(
		context.Background(),
		validConfig,
		func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
			loads++
			return aws.Config{}, nil
		},
		func(*awss3.Client, string, ...filesystemS3.Option) (*filesystemS3.Adapter, error) {
			return nil, errInjected
		},
	)
	if !errors.Is(err, errInjected) || loads != 1 {
		t.Fatalf("newWithLoaderAndTransport() error = %v, loads = %d", err, loads)
	}
}

func TestLoadAppliesConfiguredPrefixToTransportRequests(t *testing.T) {
	httpClient := &recordingHTTPClient{}
	configuration := validConfig
	configuration.Prefix = "tenant/files"
	adapter, err := Load(context.Background(), configuration, WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	logicalPath, err := filesystem.ParsePath("object.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write(context.Background(), logicalPath, bytes.NewReader([]byte("contents")), filesystem.WriteOptions{}); err == nil {
		t.Fatal("Write() error = nil")
	}
	if httpClient.path != "/bucket/tenant/files/object.txt" {
		t.Fatalf("request path = %q", httpClient.path)
	}
}

func TestLoadRejectsInvalidInputsBeforeConfigurationLoad(t *testing.T) {
	var nilContext context.Context
	optionCalls := 0
	option := func(*settings) { optionCalls++ }
	if _, err := Load(nilContext, Config{}, option); !errors.Is(err, filesystem.ErrContextRequired) {
		t.Fatalf("Load(nil) error = %v", err)
	}
	if _, err := New(nilContext, Config{}, option); !errors.Is(err, filesystem.ErrContextRequired) {
		t.Fatalf("New(nil) error = %v", err)
	}
	if optionCalls != 0 {
		t.Fatalf("nil-context load invoked options %d times", optionCalls)
	}
	preCanceled, preCancel := context.WithCancel(context.Background())
	preCancel()
	if _, err := Load(preCanceled, Config{}, option); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(canceled, invalid config) error = %v", err)
	}
	if _, err := New(preCanceled, Config{}, option); !errors.Is(err, context.Canceled) {
		t.Fatalf("New(canceled, invalid config) error = %v", err)
	}
	if optionCalls != 0 {
		t.Fatalf("pre-canceled load invoked options %d times", optionCalls)
	}

	var typedNilClient *stubHTTPClient
	for name, options := range map[string][]Option{
		"nil option":              {nil},
		"mixed nil option":        {WithMaxListEntries(1), nil},
		"nil transfer callback":   {WithTransferOptions(nil)},
		"mixed transfer callback": {WithTransferOptions(func(*transfermanager.Options) {}, nil)},
		"literal nil HTTP client": {WithHTTPClient(nil)},
		"typed-nil HTTP client":   {WithHTTPClient(typedNilClient)},
	} {
		loads := 0
		_, err := newWithLoader(context.Background(), validConfig, func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
			loads++
			return aws.Config{}, nil
		}, options...)
		if err == nil {
			t.Fatalf("newWithLoader(%s) error = nil", name)
		}
		if loads != 0 {
			t.Fatalf("newWithLoader(%s) loads = %d, want 0", name, loads)
		}
	}

	configuration := validConfig
	configuration.Prefix = "../escape"
	loads := 0
	if _, err := newWithLoader(context.Background(), configuration, func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		return aws.Config{}, nil
	}); err == nil {
		t.Fatal("newWithLoader(invalid prefix) error = nil")
	}
	if loads != 0 {
		t.Fatalf("newWithLoader(invalid prefix) loads = %d, want 0", loads)
	}
	if isNilHTTPClient(valueHTTPClient{}) {
		t.Fatal("isNilHTTPClient(value) = true")
	}
}

func TestLoadHonorsCancellationAndLoadsOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loads := 0
	loader := func(ctx context.Context, _ ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		return aws.Config{}, ctx.Err()
	}
	if _, err := newWithLoader(ctx, validConfig, loader); !errors.Is(err, context.Canceled) {
		t.Fatalf("newWithLoader(pre-canceled) error = %v", err)
	}
	if loads != 0 {
		t.Fatalf("newWithLoader(pre-canceled) loads = %d, want 0", loads)
	}

	ctx, cancel = context.WithCancel(context.Background())
	loads = 0
	_, err := newWithLoader(ctx, validConfig, func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		return aws.Config{}, nil
	}, func(*settings) { cancel() })
	if !errors.Is(err, context.Canceled) || loads != 0 {
		t.Fatalf("newWithLoader(canceled by option) error = %v, loads = %d", err, loads)
	}

	loads = 0
	_, err = newWithLoader(context.Background(), validConfig, func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		return aws.Config{}, errors.New("load failed")
	})
	if err == nil || loads != 1 {
		t.Fatalf("newWithLoader() error = %v, loads = %d", err, loads)
	}

	ctx, cancel = context.WithCancel(context.Background())
	loads = 0
	_, err = newWithLoader(ctx, validConfig, func(ctx context.Context, _ ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		cancel()
		return aws.Config{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || loads != 1 {
		t.Fatalf("newWithLoader(canceled during load) error = %v, loads = %d", err, loads)
	}

	ctx, cancel = context.WithCancel(context.Background())
	loads = 0
	_, err = newWithLoader(ctx, validConfig, func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		cancel()
		return aws.Config{}, nil
	})
	if !errors.Is(err, context.Canceled) || loads != 1 {
		t.Fatalf("newWithLoader(canceled after load) error = %v, loads = %d", err, loads)
	}
}

func TestTransferOptionsAreDefensivelyCopied(t *testing.T) {
	first := func(*transfermanager.Options) {}
	callbacks := []func(*transfermanager.Options){first}
	configuration := settings{}
	WithTransferOptions(callbacks...)(&configuration)
	callbacks[0] = nil
	if len(configuration.transferOptions) != 1 || configuration.transferOptions[0] == nil {
		t.Fatal("WithTransferOptions retained the caller slice")
	}
}

type stubHTTPClient struct{}

func (*stubHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected HTTP request")
}

type recordingHTTPClient struct {
	path string
}

func (client *recordingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	client.path = request.URL.Path
	return nil, errInjected
}

type valueHTTPClient struct{}

func (valueHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected HTTP request")
}
