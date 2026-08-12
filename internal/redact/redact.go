// Package redact removes credential-shaped values from errors while retaining
// their original cause for errors.Is and errors.As.
package redact

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var urlSchemePattern = regexp.MustCompile(`(?i)https?://`)

// Error sanitizes URLs, authentication headers, and explicitly supplied
// secrets while preserving err as the unwrap target.
func Error(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := redactURLs(err.Error())
	for _, header := range []string{
		"authorization:",
		"proxy-authorization:",
		"x-amz-security-token:",
		"x-amz-credential:",
		"x-amz-signature:",
	} {
		message = redactHeaders(message, header)
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return sanitizedError{cause: err, message: message}
}

type sanitizedError struct {
	cause   error
	message string
}

func (e sanitizedError) Error() string { return e.message }

func (e sanitizedError) Unwrap() error { return e.cause }

func redactURLs(message string) string {
	var sanitized strings.Builder
	for message != "" {
		index, found := firstURLIndex(message)
		if !found {
			sanitized.WriteString(message)
			return sanitized.String()
		}
		sanitized.WriteString(message[:index])
		message = message[index:]
		end := len(message)
		for offset, character := range message {
			if unicode.IsSpace(character) {
				end = offset
				break
			}
		}
		raw := message[:end]
		parsed, err := url.Parse(raw)
		if err != nil {
			sanitized.WriteString("[REDACTED URL]")
		} else {
			parsed.User = nil
			if parsed.RawQuery != "" {
				parsed.RawQuery = "REDACTED"
			}
			sanitized.WriteString(parsed.String())
		}
		message = message[end:]
	}
	return sanitized.String()
}

func redactHeaders(message, header string) string {
	for {
		index, found := indexFold(message, header)
		if !found {
			return message
		}
		prefix := message[:index]
		remainder := message[index:]
		_, suffix, found := strings.Cut(remainder, "\n")
		if found {
			suffix = "\n" + suffix
		}
		message = prefix + "[REDACTED HEADER]" + suffix
	}
}

func indexFold(value, substring string) (int, bool) {
	location := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(substring)).FindStringIndex(value)
	if location == nil {
		return 0, false
	}
	return location[0], true
}

func firstURLIndex(message string) (int, bool) {
	location := urlSchemePattern.FindStringIndex(message)
	if location == nil {
		return 0, false
	}
	return location[0], true
}
