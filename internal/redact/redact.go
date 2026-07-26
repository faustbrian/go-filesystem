// Package redact removes credential-shaped values from errors while retaining
// their original cause for errors.Is and errors.As.
package redact

import (
	"net/url"
	"strings"
	"unicode"
)

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
	for len(message) > 0 {
		index := indexFold(message, "https://")
		if httpIndex := indexFold(message, "http://"); httpIndex >= 0 && (index < 0 || httpIndex < index) {
			index = httpIndex
		}
		if index < 0 {
			sanitized.WriteString(message)
			break
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
		index := indexFold(message, header)
		if index < 0 {
			return message
		}
		end := strings.IndexByte(message[index:], '\n')
		if end < 0 {
			end = len(message)
		} else {
			end += index
		}
		message = message[:index] + "[REDACTED HEADER]" + message[end:]
	}
}

func indexFold(value, substring string) int {
	for index := 0; index+len(substring) <= len(value); index++ {
		if strings.EqualFold(value[index:index+len(substring)], substring) {
			return index
		}
	}
	return -1
}
