// Package redact removes credentials before log records leave the process.
package redact

import (
	"regexp"
	"strings"
)

var (
	pemPattern    = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	jwtPattern    = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	bearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]+`)
)

var secretKeys = map[string]struct{}{
	"private_key": {}, "privatekey": {}, "authorization": {}, "jwt": {},
	"token": {}, "access_token": {}, "secret": {},
}

// Text redacts private keys, bearer credentials, and JWTs.
func Text(value string) string {
	value = pemPattern.ReplaceAllString(value, "[redacted-private-key]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [redacted]")
	return jwtPattern.ReplaceAllString(value, "[redacted-jwt]")
}

// Value recursively redacts strings and sensitive map keys.
func Value(value any) any {
	switch typed := value.(type) {
	case string:
		return Text(typed)
	case map[string]any:
		cleaned := make(map[string]any, len(typed))
		for key, item := range typed {
			if _, secret := secretKeys[strings.ToLower(key)]; secret {
				cleaned[key] = "[redacted]"
			} else {
				cleaned[key] = Value(item)
			}
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(typed))
		for i, item := range typed {
			cleaned[i] = Value(item)
		}
		return cleaned
	default:
		return value
	}
}
