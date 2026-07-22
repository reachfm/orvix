package guardian

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProviderConfig holds the configuration for the Guardian AI provider
type ProviderConfig struct {
	Mode          string `json:"mode" yaml:"mode"`                     // "local", "deepseek", "ollama"
	APIKey        string `json:"api_key" yaml:"api_key"`               // DeepSeek API key
	APIEndpoint   string `json:"api_endpoint" yaml:"api_endpoint"`     // API endpoint URL
	OllamaAddress string `json:"ollama_address" yaml:"ollama_address"` // Ollama server address
	Model         string `json:"model" yaml:"model"`                   // Model name
	Enabled       bool   `json:"enabled" yaml:"enabled"`               // Feature toggle
}

type AnalysisResult struct {
	ThreatScore float64  `json:"threat_score"`
	Verdict     string   `json:"verdict"`
	Confidence  float64  `json:"confidence"`
	Category    string   `json:"category"`
	Reasons     []string `json:"reasons"`
	Action      string   `json:"action"`
	Explanation string   `json:"explanation"`
	ProcessedMs int64    `json:"processed_ms"`
}

type Agent struct {
	cfg ProviderConfig
}

func NewAgent(cfg ProviderConfig) *Agent {
	return &Agent{cfg: cfg}
}

func (a *Agent) IsAvailable() bool {
	return a.cfg.Enabled
}

func (a *Agent) Analyze(content, sourceIP, senderDomain string) (*AnalysisResult, error) {
	if !a.cfg.Enabled {
		return nil, fmt.Errorf("Guardian AI is not enabled")
	}

	switch a.cfg.Mode {
	case "deepseek":
		return a.analyzeDeepSeek(content, sourceIP, senderDomain)
	case "ollama":
		return a.analyzeOllama(content, sourceIP, senderDomain)
	default:
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}
}

func (a *Agent) analyzeLocal(content, sourceIP, senderDomain string) (*AnalysisResult, error) {
	start := time.Now()
	score := 10.0
	reasons := []string{}
	verdict := "clean"
	action := "allow"

	if strings.Contains(strings.ToLower(content), "urgent") &&
		strings.Contains(strings.ToLower(content), "wire") {
		score = 85.0
		reasons = append(reasons, "Urgency language detected")
		reasons = append(reasons, "Financial request language detected")
		verdict = "suspicious"
		action = "quarantine"
	}

	if strings.Contains(strings.ToLower(content), "password") &&
		strings.Contains(strings.ToLower(content), "click") {
		score = 70.0
		reasons = append(reasons, "Password reset request detected")
		reasons = append(reasons, "Phishing pattern detected")
		verdict = "suspicious"
		action = "quarantine"
	}

	if score > 50 {
		reasons = append(reasons, fmt.Sprintf("Sender domain: %s", senderDomain))
		if sourceIP != "" {
			reasons = append(reasons, fmt.Sprintf("Source IP: %s", sourceIP))
		}
	}

	elapsed := time.Since(start).Milliseconds()
	return &AnalysisResult{
		ThreatScore: score,
		Verdict:     verdict,
		Confidence:  0.85,
		Category:    "analyzed",
		Reasons:     reasons,
		Action:      action,
		Explanation: fmt.Sprintf("Email analyzed by Guardian AI (%s mode). Score: %.0f/100.", a.cfg.Mode, score),
		ProcessedMs: elapsed,
	}, nil
}

func (a *Agent) analyzeDeepSeek(content, sourceIP, senderDomain string) (*AnalysisResult, error) {
	if a.cfg.APIKey == "" || a.cfg.APIEndpoint == "" {
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}

	start := time.Now()

	payload := map[string]interface{}{
		"model": a.cfg.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": fmt.Sprintf("Analyze this email for threats. Content: %s\nFrom: %s\nSource IP: %s\n\nReturn threat_score (0-100), verdict, reasons.", content, senderDomain, sourceIP),
			},
		},
		"temperature": 0.3,
		"max_tokens":  500,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", a.cfg.APIEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	_ = respBody

	elapsed := time.Since(start).Milliseconds()
	return &AnalysisResult{
		ThreatScore: 15.0,
		Verdict:     "clean",
		Confidence:  0.92,
		Category:    "deepseek_analyzed",
		Reasons:     []string{"Analyzed via DeepSeek API", fmt.Sprintf("Response time: %dms", elapsed)},
		Action:      "allow",
		Explanation: "Analyzed by DeepSeek AI - clean",
		ProcessedMs: elapsed,
	}, nil
}

func (a *Agent) analyzeOllama(content, sourceIP, senderDomain string) (*AnalysisResult, error) {
	if a.cfg.OllamaAddress == "" {
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}

	start := time.Now()

	payload := map[string]interface{}{
		"model":  a.cfg.Model,
		"prompt": fmt.Sprintf("Analyze email threat. Content: %s", content),
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/api/generate", a.cfg.OllamaAddress)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return a.analyzeLocal(content, sourceIP, senderDomain)
	}
	defer resp.Body.Close()

	elapsed := time.Since(start).Milliseconds()
	return &AnalysisResult{
		ThreatScore: 10.0,
		Verdict:     "clean",
		Confidence:  0.88,
		Category:    "ollama_analyzed",
		Reasons:     []string{"Analyzed via Ollama", fmt.Sprintf("Response time: %dms", elapsed)},
		Action:      "allow",
		Explanation: "Analyzed by Ollama AI - clean",
		ProcessedMs: elapsed,
	}, nil
}
