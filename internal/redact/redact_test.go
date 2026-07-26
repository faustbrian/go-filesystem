package redact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/faustbrian/golib/pkg/filesystem/internal/redact"
)

func TestErrorRedactsCredentialsAndPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New(
		"https://user:password@example.test/object?signature=secret-query\n" +
			"Authorization: Bearer secret-header\n" +
			"X-Amz-Security-Token: secret-token\n" +
			"explicit-secret",
	)
	err := redact.Error(cause, "explicit-secret")
	if !errors.Is(err, cause) {
		t.Fatalf("Error() did not preserve cause: %v", err)
	}
	for _, secret := range []string{
		"user",
		"password",
		"secret-query",
		"secret-header",
		"secret-token",
		"explicit-secret",
	} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Error() leaked %q: %v", secret, err)
		}
	}
}

func TestErrorAcceptsNil(t *testing.T) {
	t.Parallel()

	if err := redact.Error(nil, "secret"); err != nil {
		t.Fatalf("Error(nil) = %v", err)
	}
}

func TestErrorRedactsMalformedURL(t *testing.T) {
	t.Parallel()

	err := redact.Error(errors.New("remote failure at http://%"))
	if strings.Contains(err.Error(), "%") || !strings.Contains(err.Error(), "REDACTED URL") {
		t.Fatalf("Error() = %v", err)
	}
}

func FuzzErrorRedaction(f *testing.F) {
	for _, seed := range []string{
		"plain error",
		"https://example.test/object?X-Amz-Signature=fuzz-secret",
		"Authorization: Bearer fuzz-secret",
		"prefix fuzz-secret suffix",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, message string) {
		err := redact.Error(errors.New(message), "fuzz-secret")
		if strings.Contains(err.Error(), "fuzz-secret") {
			t.Fatalf("Error() leaked explicit secret: %v", err)
		}
	})
}
