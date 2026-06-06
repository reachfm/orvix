package compose

import (
	"testing"
	"go.uber.org/zap"
)

func TestNewStreamer(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	s := NewStreamer("test-key", "deepseek-chat", logger)
	if s == nil {
		t.Fatal("NewStreamer returned nil")
	}
	if s.apiKey != "test-key" {
		t.Fatalf("expected apiKey 'test-key', got %s", s.apiKey)
	}
}

func TestCompletionRequestDefaults(t *testing.T) {
	req := &CompletionRequest{
		Context: "Previous email thread",
		Prompt:  "Write a reply",
		Tone:    "formal",
		MaxTokens: 500,
		Action: "compose",
	}
	if req.Tone != "formal" {
		t.Fatalf("unexpected tone: %s", req.Tone)
	}
	if req.MaxTokens != 500 {
		t.Fatalf("unexpected max tokens: %d", req.MaxTokens)
	}
}

func TestStreamerNoAPIKey(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	s := NewStreamer("", "deepseek-chat", logger)

	text, err := s.Complete(nil, &CompletionRequest{
		Context: "test", Prompt: "test", Tone: "formal", Action: "compose",
	})
	if err != nil {
		t.Fatalf("Complete should not fail without API key: %v", err)
	}
	if text == "" {
		t.Fatal("expected error message without API key")
	}
}
