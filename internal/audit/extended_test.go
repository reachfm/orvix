package audit

import (
	"strings"
	"testing"
)

func TestSanitizeJSONRedactsNestedSecretsCaseInsensitively(t *testing.T) {
	input := `{"outer":{"Password":"hunter2","items":[{"SESSION_TOKEN":"secret-token"}]},"safe":"visible"}`
	got := sanitizeJSON(input, sensitiveFields)
	for _, secret := range []string{"hunter2", "secret-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized audit metadata leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, `"safe":"visible"`) {
		t.Fatalf("sanitizer removed safe metadata: %s", got)
	}
}

func TestSanitizeJSONMalformedInputFailsClosed(t *testing.T) {
	input := `{"password":"secret"`
	got := sanitizeJSON(input, sensitiveFields)
	if strings.Contains(got, "secret") || strings.Contains(got, input) {
		t.Fatalf("malformed audit metadata leaked input: %s", got)
	}
	if got != `"[REDACTED: invalid audit metadata]"` {
		t.Fatalf("unexpected malformed metadata marker: %s", got)
	}
}
