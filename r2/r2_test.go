package r2

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
)

func TestNewBuildsCloudflareProfile(t *testing.T) {
	t.Parallel()

	adapter, err := New(context.Background(), Config{
		AccountID:       "0123456789abcdef0123456789abcdef",
		Bucket:          "bucket",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		Prefix:          "tenant/files",
	})
	if err != nil {
		t.Fatal(err)
	}

	profile := adapter.Profile()
	if profile.Endpoint != "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com" {
		t.Fatalf("Profile().Endpoint = %q", profile.Endpoint)
	}
	if profile.Region != "auto" {
		t.Fatalf("Profile().Region = %q, want auto", profile.Region)
	}
	if !profile.PathStyle {
		t.Fatal("Profile().PathStyle = false, want true")
	}
	if profile.SupportsACL {
		t.Fatal("Profile().SupportsACL = true, want false")
	}
	if profile.SupportsCopyChecksums {
		t.Fatal("Profile().SupportsCopyChecksums = true, want false")
	}
	if !profile.MultipartRequiresUniformPartSize {
		t.Fatal("Profile().MultipartRequiresUniformPartSize = false, want true")
	}

	clientOptions := adapter.client.Options()
	if clientOptions.Region != "auto" || !clientOptions.UsePathStyle {
		t.Fatalf("client options region = %q, path style = %v", clientOptions.Region, clientOptions.UsePathStyle)
	}
	if aws.ToString(clientOptions.BaseEndpoint) != profile.Endpoint {
		t.Fatalf("client BaseEndpoint = %q", aws.ToString(clientOptions.BaseEndpoint))
	}
	credentials, err := clientOptions.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessKeyID != "access-key" || credentials.SecretAccessKey != "secret-key" {
		t.Fatal("client did not use explicit R2 credentials")
	}
}

func TestNewRejectsUnsafeOrIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	valid := Config{
		AccountID:       "0123456789abcdef0123456789abcdef",
		Bucket:          "bucket",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
	}
	tests := map[string]func(*Config, *[]Option){
		"missing account":    func(config *Config, _ *[]Option) { config.AccountID = "" },
		"malformed account":  func(config *Config, _ *[]Option) { config.AccountID = "not-an-account" },
		"missing bucket":     func(config *Config, _ *[]Option) { config.Bucket = "" },
		"missing access key": func(config *Config, _ *[]Option) { config.AccessKeyID = "" },
		"missing secret":     func(config *Config, _ *[]Option) { config.SecretAccessKey = "" },
		"http endpoint": func(_ *Config, options *[]Option) {
			*options = append(*options, WithEndpoint("http://r2.example.test"))
		},
		"endpoint credentials": func(_ *Config, options *[]Option) {
			*options = append(*options, WithEndpoint("https://user:password@r2.example.test"))
		},
		"endpoint path": func(_ *Config, options *[]Option) {
			*options = append(*options, WithEndpoint("https://r2.example.test/private"))
		},
		"endpoint query": func(_ *Config, options *[]Option) {
			*options = append(*options, WithEndpoint("https://r2.example.test?token=secret"))
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			configuration := valid
			var options []Option
			mutate(&configuration, &options)
			_, err := New(context.Background(), configuration, options...)
			if err == nil {
				t.Fatal("New() error = nil")
			}
			if strings.Contains(err.Error(), valid.SecretAccessKey) {
				t.Fatalf("New() error leaked secret: %v", err)
			}
		})
	}
}

func TestDevelopmentEndpointOnlyAllowsLoopbackHTTP(t *testing.T) {
	t.Parallel()

	configuration := Config{
		AccountID:       "0123456789abcdef0123456789abcdef",
		Bucket:          "bucket",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
	}
	adapter, err := New(
		context.Background(),
		configuration,
		WithDevelopmentEndpoint("http://127.0.0.1:9000"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Profile().Endpoint != "http://127.0.0.1:9000" {
		t.Fatalf("Profile().Endpoint = %q", adapter.Profile().Endpoint)
	}
	if _, err := New(context.Background(), configuration, WithDevelopmentEndpoint("http://192.0.2.1:9000")); err == nil {
		t.Fatal("non-loopback development endpoint was accepted")
	}
}

func TestCapabilitiesExposeR2Differences(t *testing.T) {
	t.Parallel()

	adapter, err := New(context.Background(), Config{
		AccountID:       "0123456789abcdef0123456789abcdef",
		Bucket:          "bucket",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityMetadata,
		filesystem.CapabilityMultipart,
		filesystem.CapabilityTemporaryURL,
	} {
		if !adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = false", capability)
		}
	}
	for _, capability := range []filesystem.Capability{
		filesystem.CapabilityVisibility,
		filesystem.CapabilityChecksum,
		filesystem.CapabilityMove,
	} {
		if adapter.Capabilities().Supports(capability) {
			t.Errorf("Capabilities().Supports(%q) = true", capability)
		}
	}
}
