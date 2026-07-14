package compose

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// CompletionRequest represents a streaming completion request.
type CompletionRequest struct {
	Context   string `json:"context"`
	Prompt    string `json:"prompt"`
	Tone      string `json:"tone"`
	MaxTokens int    `json:"max_tokens"`
	Action    string `json:"action"`
}

// Streamer handles streaming AI completions for Smart Compose.
type Streamer struct {
	apiKey     string
	model      string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewStreamer creates a new Smart Compose streamer.
func NewStreamer(apiKey, model string, logger *zap.Logger) *Streamer {
	return &Streamer{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
}

// Complete generates a non-streaming completion.
func (s *Streamer) Complete(ctx context.Context, req *CompletionRequest) (string, error) {
	if s.apiKey == "" {
		return "Smart Compose AI requires a DeepSeek API key. Configure ORVIX_DEEPSEEK_API_KEY.", nil
	}

	systemPrompt := fmt.Sprintf("You are an email writing assistant. Tone: %s. Write concise, professional email content.", req.Tone)
	userPrompt := fmt.Sprintf("Context: %s\n\nWrite: %s", req.Context, req.Prompt)

	if req.Action == "summarize" {
		systemPrompt = "You are an email summarizer. Provide a concise summary of the following email thread."
		userPrompt = req.Context
	}
	if req.Action == "rewrite" {
		systemPrompt = fmt.Sprintf("You are an email editor. Rewrite the following email in a %s tone while preserving all key information.", req.Tone)
		userPrompt = req.Prompt
	}

	payload := map[string]interface{}{
		"model": s.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens": 500,
	}

	data, _ := json.Marshal(payload)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.deepseek.com/v1/chat/completions", bytes.NewReader(data))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("deepseek request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			}
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no completion returned")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

// Stream generates streaming completion via SSE.
func (s *Streamer) Stream(ctx context.Context, req *CompletionRequest, onChunk func(string)) error {
	if s.apiKey == "" {
		onChunk("Smart Compose AI requires a DeepSeek API key.")
		return nil
	}

	payload := map[string]interface{}{
		"model": s.model,
		"messages": []map[string]string{
			{"role": "system", "content": fmt.Sprintf("You are an email writing assistant. Tone: %s. Write concise, professional email content.", req.Tone)},
			{"role": "user", "content": fmt.Sprintf("Context: %s\n\nWrite: %s", req.Context, req.Prompt)},
		},
		"stream":     true,
		"max_tokens": req.MaxTokens,
	}

	data, _ := json.Marshal(payload)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.deepseek.com/v1/chat/completions", bytes.NewReader(data))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		d := strings.TrimPrefix(line, "data: ")
		if d == "[DONE]" {
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				}
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(d), &chunk) == nil && len(chunk.Choices) > 0 {
			onChunk(chunk.Choices[0].Delta.Content)
		}
	}
}
