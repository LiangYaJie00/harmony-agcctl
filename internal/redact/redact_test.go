package redact

import (
	"strings"
	"testing"
)

func TestRedactsPrivateKeyBearerAndJWT(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----\n"
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	redacted := Text("header " + pem + " Authorization: Bearer " + token + " raw " + token)
	if strings.Contains(redacted, "SECRET") || strings.Contains(redacted, token) || !strings.Contains(redacted, "Bearer [redacted]") {
		t.Fatalf("redacted = %s", redacted)
	}
}

func TestRedactsSecretMappingKeys(t *testing.T) {
	cleaned := Value(map[string]any{"private_key": "abc", "taskId": "PUBLISH-1", "nested": []any{map[string]any{"authorization": "Bearer abc"}}}).(map[string]any)
	if cleaned["private_key"] != "[redacted]" || cleaned["taskId"] != "PUBLISH-1" {
		t.Fatalf("cleaned = %#v", cleaned)
	}
}
