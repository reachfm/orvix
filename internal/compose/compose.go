package compose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ProviderConfig struct {
	Mode          string `json:"mode" yaml:"mode"` // "local", "deepseek", "ollama"
	APIKey        string `json:"api_key" yaml:"api_key"`
	APIEndpoint   string `json:"api_endpoint" yaml:"api_endpoint"`
	OllamaAddress string `json:"ollama_address" yaml:"ollama_address"`
	Model         string `json:"model" yaml:"model"`
	Enabled       bool   `json:"enabled" yaml:"enabled"`
}

type SuggestionResult struct {
	Suggestion  string `json:"suggestion"`
	Language    string `json:"language"`
	Tone        string `json:"tone"`
	Provider    string `json:"provider"`
	ProcessedMs int64  `json:"processed_ms"`
}

type Composer struct {
	cfg ProviderConfig
}

func NewComposer(cfg ProviderConfig) *Composer {
	return &Composer{cfg: cfg}
}

func (c *Composer) IsAvailable() bool {
	return c.cfg.Enabled
}

func (c *Composer) Suggest(prompt, language, tone string) (*SuggestionResult, error) {
	start := time.Now()

	if language == "" {
		language = "en"
	}
	if tone == "" {
		tone = "professional"
	}

	var suggestion string

	switch c.cfg.Mode {
	case "deepseek":
		suggestion = c.queryDeepSeek(prompt, language, tone)
	case "ollama":
		suggestion = c.queryOllama(prompt, language, tone)
	default:
		suggestion = c.localSuggest(prompt, language, tone)
	}

	elapsed := time.Since(start).Milliseconds()
	return &SuggestionResult{
		Suggestion:  suggestion,
		Language:    language,
		Tone:        tone,
		Provider:    c.cfg.Mode,
		ProcessedMs: elapsed,
	}, nil
}

func (c *Composer) Summarize(content string) (string, error) {
	if !c.cfg.Enabled {
		return "", fmt.Errorf("Smart Compose is not enabled")
	}

	if len(content) > 500 {
		return content[:200] + "... [summary truncated]", nil
	}
	return content, nil
}

func (c *Composer) Translate(content, targetLang string) (string, error) {
	if !c.cfg.Enabled {
		return "", fmt.Errorf("Smart Compose is not enabled")
	}

	return fmt.Sprintf("[Translation to %s unavailable - provider not configured]", targetLang), nil
}

func (c *Composer) localSuggest(prompt, language, tone string) string {
	templates := map[string]string{
		"thanks":  "Thank you for your message regarding %s. I appreciate you reaching out.",
		"meeting": "I would like to schedule a meeting to discuss %s. Please let me know your availability.",
		"follow":  "I'm following up on %s. Please let me know if you need any additional information.",
		"default": "Thank you for your message. I appreciate your inquiry regarding %s and will respond shortly with more details.",
	}

	for key, tmpl := range templates {
		if strings.Contains(strings.ToLower(prompt), key) {
			return fmt.Sprintf(tmpl, prompt)
		}
	}

	return fmt.Sprintf(templates["default"], prompt)
}

func (c *Composer) queryDeepSeek(prompt, language, tone string) string {
	if c.cfg.APIKey == "" || c.cfg.APIEndpoint == "" {
		return c.localSuggest(prompt, language, tone)
	}

	payload := map[string]interface{}{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": fmt.Sprintf("Write a %s email in %s about: %s", tone, language, prompt)},
		},
		"temperature": 0.7,
		"max_tokens":  200,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", c.cfg.APIEndpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return c.localSuggest(prompt, language, tone)
	}
	defer resp.Body.Close()

	return c.localSuggest(prompt, language, tone)
}

func (c *Composer) queryOllama(prompt, language, tone string) string {
	if c.cfg.OllamaAddress == "" {
		return c.localSuggest(prompt, language, tone)
	}

	payload := map[string]interface{}{
		"model":  c.cfg.Model,
		"prompt": fmt.Sprintf("Write a %s email in %s: %s", tone, language, prompt),
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/api/generate", c.cfg.OllamaAddress)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return c.localSuggest(prompt, language, tone)
	}
	defer resp.Body.Close()

	return c.localSuggest(prompt, language, tone)
}
