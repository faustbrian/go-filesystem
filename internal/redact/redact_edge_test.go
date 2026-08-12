package redact

import (
	"errors"
	"testing"
)

func TestFirstURLIndexBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		message string
		want    int
		found   bool
	}{
		{message: "none"},
		{message: "http://example.test", found: true},
		{message: "HTTPS://example.test", found: true},
		{message: "x https://secure.test http://plain.test", want: 2, found: true},
		{message: "x http://plain.test https://secure.test", want: 2, found: true},
	} {
		got, found := firstURLIndex(test.message)
		if got != test.want || found != test.found {
			t.Errorf("firstURLIndex(%q) = %d, %t; want %d, %t", test.message, got, found, test.want, test.found)
		}
	}
}

func TestURLAndHeaderRedactionBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		message string
		want    string
	}{
		{message: "plain", want: "plain"},
		{message: "before http://user:pass@example.test/path?secret=value after", want: "before http://example.test/path?REDACTED after"},
		{message: "http://one.test https://two.test", want: "http://one.test https://two.test"},
		{message: "http://%", want: "[REDACTED URL]"},
	} {
		if got := redactURLs(test.message); got != test.want {
			t.Errorf("redactURLs(%q) = %q, want %q", test.message, got, test.want)
		}
	}

	for _, test := range []struct {
		message string
		want    string
	}{
		{message: "plain", want: "plain"},
		{message: "Authorization: secret", want: "[REDACTED HEADER]"},
		{message: "before\nAUTHORIZATION: first\nafter", want: "before\n[REDACTED HEADER]\nafter"},
		{message: "Authorization: first\nAuthorization: second", want: "[REDACTED HEADER]\n[REDACTED HEADER]"},
	} {
		if got := redactHeaders(test.message, "authorization:"); got != test.want {
			t.Errorf("redactHeaders(%q) = %q, want %q", test.message, got, test.want)
		}
	}
}

func TestSanitizedErrorContract(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause")
	err := sanitizedError{cause: cause, message: "safe"}
	if err.Error() != "safe" || err.Unwrap() != cause {
		t.Fatalf("sanitizedError = %q, unwrap %v", err.Error(), err.Unwrap())
	}
}
