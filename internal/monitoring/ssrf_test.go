package monitoring

import (
	"strings"
	"testing"
)

func TestValidateWebhookURL_ValidHTTPS(t *testing.T) {
	if err := ValidateWebhookURL("https://example.com/webhook"); err != nil {
		t.Fatalf("valid HTTPS URL should pass: %v", err)
	}
}

func TestValidateWebhookURL_HTTPRejected(t *testing.T) {
	if err := ValidateWebhookURL("http://hooks.example.com/webhook"); err == nil {
		t.Fatal("HTTP URL must be rejected")
	}
}

func TestValidateWebhookURL_LoopbackIPv4(t *testing.T) {
	if err := ValidateWebhookURL("https://127.0.0.1/hook"); err == nil {
		t.Fatal("loopback IPv4 must be rejected")
	}
}

func TestValidateWebhookURL_LoopbackIPv6(t *testing.T) {
	if err := ValidateWebhookURL("https://[::1]/hook"); err == nil {
		t.Fatal("loopback IPv6 must be rejected")
	}
}

func TestValidateWebhookURL_Private10(t *testing.T) {
	if err := ValidateWebhookURL("https://10.0.0.1/hook"); err == nil {
		t.Fatal("private 10.x must be rejected")
	}
}

func TestValidateWebhookURL_Private172_16(t *testing.T) {
	if err := ValidateWebhookURL("https://172.16.0.1/hook"); err == nil {
		t.Fatal("private 172.16.x must be rejected")
	}
}

func TestValidateWebhookURL_Private192_168(t *testing.T) {
	if err := ValidateWebhookURL("https://192.168.1.1/hook"); err == nil {
		t.Fatal("private 192.168.x must be rejected")
	}
}

func TestValidateWebhookURL_LinkLocal(t *testing.T) {
	if err := ValidateWebhookURL("https://169.254.169.254/hook"); err == nil {
		t.Fatal("link-local must be rejected")
	}
}

func TestValidateWebhookURL_EmptyURL(t *testing.T) {
	if err := ValidateWebhookURL(""); err == nil {
		t.Fatal("empty URL must be rejected")
	}
}

func TestValidateWebhookURL_MalformedURL(t *testing.T) {
	err := ValidateWebhookURL("://bad-url")
	if err == nil {
		t.Fatal("malformed URL must be rejected")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("expected 'invalid URL' error, got: %v", err)
	}
}
