// Package r2 provides a first-class Cloudflare R2 adapter profile over the S3
// transport. It exposes R2's configuration and capability differences instead
// of treating R2 as generic Amazon S3.
package r2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	"github.com/faustbrian/golib/pkg/filesystem/internal/redact"
	filesystemS3 "github.com/faustbrian/golib/pkg/filesystem/s3"
)

var accountIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// Config contains the credentials and bucket identity required by R2.
type Config struct {
	// AccountID is the 32-hex-character Cloudflare account identifier.
	AccountID string
	// Bucket is the R2 bucket name.
	Bucket string
	// AccessKeyID is a scoped R2 S3 API access key.
	AccessKeyID string
	// SecretAccessKey is the matching R2 S3 API secret.
	SecretAccessKey string
	// Prefix places every logical path beneath one normalized key prefix.
	Prefix string
}

// Profile documents transport and semantic differences from Amazon S3.
type Profile struct {
	// Endpoint is the validated account S3 endpoint.
	Endpoint string
	// Region is the R2 SigV4 signing region, always auto.
	Region string
	// PathStyle reports that bucket names are encoded in URL paths.
	PathStyle bool
	// SupportsACL reports R2's ACL support policy.
	SupportsACL bool
	// SupportsCopyChecksums reports checksum support on copy operations.
	SupportsCopyChecksums bool
	// MultipartRequiresUniformPartSize records R2's multipart constraint.
	MultipartRequiresUniformPartSize bool
}

// Option configures an R2 adapter.
type Option func(*settings)

type settings struct {
	endpoint            string
	developmentEndpoint bool
	maxListEntries      int
	maxMetadataEntries  int
	maxMetadataBytes    int64
	transferOptions     []func(*transfermanager.Options)
	httpClient          aws.HTTPClient
}

type configurationLoader func(
	context.Context,
	...func(*awsconfig.LoadOptions) error,
) (aws.Config, error)

// WithEndpoint overrides the account endpoint with an HTTPS S3-compatible
// endpoint. The URL may not contain credentials, a path, query, or fragment.
func WithEndpoint(endpoint string) Option {
	return func(configuration *settings) {
		configuration.endpoint = endpoint
		configuration.developmentEndpoint = false
	}
}

// WithDevelopmentEndpoint permits an HTTP endpoint only when it resolves to a
// loopback literal or localhost. It is intended for local integration tests.
func WithDevelopmentEndpoint(endpoint string) Option {
	return func(configuration *settings) {
		configuration.endpoint = endpoint
		configuration.developmentEndpoint = true
	}
}

// WithMaxListEntries bounds one List call. The default is 10,000 entries.
func WithMaxListEntries(limit int) Option {
	return func(configuration *settings) {
		configuration.maxListEntries = limit
	}
}

// WithMetadataLimits bounds user metadata accepted from callers and returned
// by R2. Defaults are 128 entries and 64 KiB of keys plus values.
func WithMetadataLimits(maxEntries int, maxBytes int64) Option {
	return func(configuration *settings) {
		configuration.maxMetadataEntries = maxEntries
		configuration.maxMetadataBytes = maxBytes
	}
}

// WithTransferOptions customizes the AWS transfer manager. R2 requires
// multipart parts to use uniform sizes except for the final part.
func WithTransferOptions(options ...func(*transfermanager.Options)) Option {
	return func(configuration *settings) {
		configuration.transferOptions = append(configuration.transferOptions, options...)
	}
}

// WithHTTPClient supplies an HTTP client with caller-owned transport and
// timeout policy.
func WithHTTPClient(client aws.HTTPClient) Option {
	return func(configuration *settings) {
		configuration.httpClient = client
	}
}

// Adapter is a Cloudflare R2 filesystem.
type Adapter struct {
	*filesystemS3.Adapter
	client  *awss3.Client
	profile Profile
}

// New creates an R2 adapter using explicit credentials and the required auto
// signing region.
func New(ctx context.Context, configuration Config, options ...Option) (*Adapter, error) {
	return newWithLoader(ctx, configuration, awsconfig.LoadDefaultConfig, options...)
}

func newWithLoader(
	ctx context.Context,
	configuration Config,
	loader configurationLoader,
	options ...Option,
) (*Adapter, error) {
	if !accountIDPattern.MatchString(configuration.AccountID) {
		return nil, errors.New("r2: account ID must be 32 hexadecimal characters")
	}
	if strings.TrimSpace(configuration.Bucket) == "" {
		return nil, errors.New("r2: bucket is required")
	}
	if configuration.AccessKeyID == "" {
		return nil, errors.New("r2: access key ID is required")
	}
	if configuration.SecretAccessKey == "" {
		return nil, errors.New("r2: secret access key is required")
	}

	configurationOptions := settings{
		maxListEntries:     10_000,
		maxMetadataEntries: 128,
		maxMetadataBytes:   64 * 1024,
	}
	for _, option := range options {
		option(&configurationOptions)
	}
	endpoint := configurationOptions.endpoint
	if endpoint == "" {
		endpoint = "https://" + strings.ToLower(configuration.AccountID) + ".r2.cloudflarestorage.com"
	}
	endpoint, err := validateEndpoint(endpoint, configurationOptions.developmentEndpoint)
	if err != nil {
		return nil, err
	}
	if configurationOptions.maxListEntries <= 0 {
		return nil, errors.New("r2: maximum list entries must be positive")
	}
	if configurationOptions.maxMetadataEntries <= 0 || configurationOptions.maxMetadataBytes <= 0 {
		return nil, errors.New("r2: metadata limits must be positive")
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			configuration.AccessKeyID,
			configuration.SecretAccessKey,
			"",
		)),
	}
	if configurationOptions.httpClient != nil {
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(configurationOptions.httpClient))
	}
	awsConfiguration, err := loader(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf(
			"r2: load AWS configuration: %w",
			redact.Error(err, configuration.AccessKeyID, configuration.SecretAccessKey),
		)
	}
	client := awss3.NewFromConfig(awsConfiguration, func(options *awss3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	transportOptions := []filesystemS3.Option{
		filesystemS3.WithMaxListEntries(configurationOptions.maxListEntries),
		filesystemS3.WithMetadataLimits(
			configurationOptions.maxMetadataEntries,
			configurationOptions.maxMetadataBytes,
		),
	}
	if configuration.Prefix != "" {
		transportOptions = append(transportOptions, filesystemS3.WithPrefix(configuration.Prefix))
	}
	if len(configurationOptions.transferOptions) > 0 {
		transportOptions = append(transportOptions, filesystemS3.WithTransferOptions(configurationOptions.transferOptions...))
	}
	transport, err := filesystemS3.NewR2Transport(client, configuration.Bucket, transportOptions...)
	if err != nil {
		return nil, err
	}
	return &Adapter{
		Adapter: transport,
		client:  client,
		profile: Profile{
			Endpoint:                         endpoint,
			Region:                           "auto",
			PathStyle:                        true,
			SupportsACL:                      false,
			SupportsCopyChecksums:            false,
			MultipartRequiresUniformPartSize: true,
		},
	}, nil
}

// Profile returns immutable R2-specific transport and semantic guarantees.
func (a *Adapter) Profile() Profile {
	return a.profile
}

// Capabilities delegates to the R2-profiled S3 transport.
func (a *Adapter) Capabilities() filesystem.CapabilitySet {
	return a.Adapter.Capabilities()
}

func validateEndpoint(raw string, development bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("r2: endpoint must be an absolute URL")
	}
	if parsed.User != nil {
		return "", errors.New("r2: endpoint must not contain credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("r2: endpoint must not contain a query or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("r2: endpoint must not contain a path")
	}
	if development {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", errors.New("r2: development endpoint must use HTTP or HTTPS")
		}
		if !isLoopback(parsed.Hostname()) {
			return "", errors.New("r2: development endpoint must use loopback")
		}
	} else if parsed.Scheme != "https" {
		return "", errors.New("r2: endpoint must use HTTPS")
	}
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
