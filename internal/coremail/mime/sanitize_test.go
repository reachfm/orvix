package mime

import (
	"strings"
	"testing"
)

func TestSanitizeRemovesScriptTags(t *testing.T) {
	input := "<p>Hello</p><script>alert('xss')</script><p>World</p>"
	output := SanitizeHTML(input)
	if strings.Contains(output, "<script") {
		t.Fatal("script tags should be removed")
	}
	if !strings.Contains(output, "<p>Hello") || !strings.Contains(output, "<p>World") {
		t.Fatal("safe HTML should be preserved")
	}
}

func TestSanitizeRemovesEventHandlers(t *testing.T) {
	input := `<button onclick="alert(1)">Click</button>`
	output := SanitizeHTML(input)
	if strings.Contains(output, "onclick") {
		t.Fatalf("event handlers should be removed, got: %s", output)
	}
}

func TestSanitizeBlocksJavascriptURL(t *testing.T) {
	input := `<a href="javascript:alert(1)">link</a>`
	output := SanitizeHTML(input)
	if strings.Contains(output, "javascript:") {
		t.Fatal("javascript: URLs should be blocked")
	}
	if !strings.Contains(output, "blocked:") {
		t.Fatal("javascript: should be replaced with blocked:")
	}
}

func TestSanitizeBlocksRemoteImages(t *testing.T) {
	input := `<img src="https://example.com/tracker.gif">`
	output := SanitizeHTML(input)
	if strings.Contains(output, " src=\"https://") {
		t.Fatal("remote image src should be replaced with data-remote-src")
	}
	if !strings.Contains(output, "data-remote-src=\"https://") {
		t.Fatal("remote src should be preserved as data-remote-src")
	}
}

func TestSanitizePreservesLocalImages(t *testing.T) {
	input := `<img src="cid:img123">`
	output := SanitizeHTML(input)
	if !strings.Contains(output, "src=\"cid:img123\"") {
		t.Fatal("local cid: images should be preserved")
	}
}

func TestSanitizeMultipleEventHandlers(t *testing.T) {
	input := `<div onmouseover="evil()" onclick="bad()" onload="hack()">content</div>`
	output := SanitizeHTML(input)
	if strings.Contains(output, "onmouseover") || strings.Contains(output, "onclick") || strings.Contains(output, "onload") {
		t.Fatal("all event handlers should be removed")
	}
	if !strings.Contains(output, "<div") {
		t.Fatal("div tag should be preserved")
	}
}

func TestSanitizeEmptyString(t *testing.T) {
	if SanitizeHTML("") != "" {
		t.Fatal("empty string should remain empty")
	}
}

func TestSanitizeNoHTML(t *testing.T) {
	input := "Just plain text"
	output := SanitizeHTML(input)
	if output != input {
		t.Fatal("plain text should pass through unchanged")
	}
}

func TestHasRemoteImagesTrue(t *testing.T) {
	if !HasRemoteImages(`<img src="https://example.com/img.png">`) {
		t.Fatal("should detect remote image")
	}
}

func TestHasRemoteImagesFalse(t *testing.T) {
	if HasRemoteImages(`<p>No images here</p>`) {
		t.Fatal("should not detect remote image")
	}
}

func TestHasRemoteImagesCID(t *testing.T) {
	if HasRemoteImages(`<img src="cid:img123">`) {
		t.Fatal("cid: images should not be considered remote")
	}
}

func TestIsProbablyHTML(t *testing.T) {
	tests := []struct {
		input  string
		expect bool
	}{
		{"<html><body>Hello</body></html>", true},
		{"<!DOCTYPE html><html>", true},
		{"<p>Hello</p>", true},
		{"<div>content</div>", true},
		{"Just plain text", false},
		{"Hello\nWorld", false},
	}
	for _, tt := range tests {
		result := IsProbablyHTML(tt.input)
		if result != tt.expect {
			t.Errorf("IsProbablyHTML(%q) = %v, want %v", tt.input, result, tt.expect)
		}
	}
}
