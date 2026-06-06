package guardian

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module implements the modules.Module interface for Guardian Agent.
type Module struct {
	cfg     *config.Config
	db      *gorm.DB
	logger  *zap.Logger
	agent   *Agent
	api     *API
	mod     *modules.Module
}

func (m *Module) ID() string { return "guardian-agent" }
func (m *Module) Version() string { return "1.0.0" }
func (m *Module) Requires() []string { return []string{"core"} }

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	m.db = db
	m.logger = cfg.GetLogger()

	apiKey := cfg.AI.DeepSeekAPIKey
	model := cfg.AI.DeepSeekModel
	m.agent = NewAgent(apiKey, model, m.logger)
	m.api = NewAPI(m.agent)

	// RC2 FIX: Skip AutoMigrate - tables are created with raw SQL

	m.logger.Info("guardian-agent module initialized")
	return nil
}

func (m *Module) Start() error {
	m.logger.Info("guardian-agent module started")
	return nil
}

func (m *Module) Stop() error {
	m.logger.Info("guardian-agent module stopped")
	return nil
}

func (m *Module) Migrate() error {
	return nil
}

// Agent returns the analysis agent for use by handlers.
func (m *Module) Agent() *Agent { return m.agent }

var _ modules.Module = (*Module)(nil)

// AnalyzeRequest represents an email analysis request.
type AnalyzeRequest struct {
	EmailID       string `json:"email_id"`
	SenderIP      string `json:"sender_ip"`
	SenderDomain  string `json:"sender_domain"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	HasAttachments bool `json:"has_attachments"`
	SPFResult     string `json:"spf_result"`
	DKIMResult    string `json:"dkim_result"`
	DMARCResult   string `json:"dmarc_result"`
}

// AnalyzeResult represents the threat analysis verdict.
type AnalyzeResult struct {
	ThreatScore float64  `json:"threat_score"`
	Verdict     string   `json:"verdict"`
	Confidence  float64  `json:"confidence"`
	Reasons     []string `json:"reasons"`
	Action      string   `json:"action"`
	Explanation string   `json:"explanation"`
}

// GuardianLog stores analysis results.
type GuardianLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	MessageID   string    `gorm:"index;not null" json:"message_id"`
	ThreatScore float64   `gorm:"not null" json:"threat_score"`
	Verdict     string    `gorm:"not null" json:"verdict"`
	Confidence  float64   `gorm:"not null;default:0" json:"confidence"`
	Reasons     string    `gorm:"type:text" json:"reasons"`
	Action      string    `gorm:"not null" json:"action"`
	CreatedAt   time.Time `json:"created_at"`
}

// Agent performs email threat analysis.
type Agent struct {
	apiKey     string
	model      string
	httpClient *http.Client
	logger     *zap.Logger
	mu         sync.Mutex
}

func NewAgent(apiKey, model string, logger *zap.Logger) *Agent {
	return &Agent{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// Analyze performs threat analysis with DeepSeek API fallback to offline scoring.
func (a *Agent) Analyze(ctx context.Context, req *AnalyzeRequest) (*AnalyzeResult, error) {
	if a.apiKey != "" {
		result, err := a.deepSeekAnalysis(ctx, req)
		if err == nil && result != nil {
			return result, nil
		}
		a.logger.Warn("deepseek analysis failed, falling back to offline", zap.Error(err))
	}
	return a.offlineAnalysis(req), nil
}

func (a *Agent) deepSeekAnalysis(ctx context.Context, req *AnalyzeRequest) (*AnalyzeResult, error) {
	payload := map[string]interface{}{
		"model": a.model,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You are a cybersecurity email threat analyzer. " +
					"Analyze the following email metadata and return a JSON response with " +
					"threat_score (0-1), verdict (safe/phishing/spam/malware), " +
					"confidence (0-1), reasons (array of strings), " +
					"action (pass/quarantine/block), and explanation.",
			},
			{
				"role": "user",
				"content": fmt.Sprintf(
					"Email ID: %s\nSender IP: %s\nSender Domain: %s\nSubject: %s\nBody: %s\nHas Attachments: %v\nSPF: %s\nDKIM: %s\nDMARC: %s",
					req.EmailID, req.SenderIP, req.SenderDomain,
					req.Subject, truncate(req.Body, 1000),
					req.HasAttachments,
					req.SPFResult, req.DKIMResult, req.DMARCResult,
				),
			},
		},
		"response_format": map[string]string{"type": "json_object"},
	}

	data, _ := json.Marshal(payload)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.deepseek.com/v1/chat/completions", bytes.NewReader(data))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var dsResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &dsResp); err != nil || len(dsResp.Choices) == 0 {
		return nil, fmt.Errorf("invalid deepseek response")
	}

	var result AnalyzeResult
	if err := json.Unmarshal([]byte(dsResp.Choices[0].Message.Content), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *Agent) offlineAnalysis(req *AnalyzeRequest) *AnalyzeResult {
	score := 0.0
	reasons := []string{}

	if req.SPFResult == "fail" {
		score += 0.3; reasons = append(reasons, "SPF check failed")
	}
	if req.DKIMResult == "fail" {
		score += 0.2; reasons = append(reasons, "DKIM signature invalid")
	}
	if req.DMARCResult == "fail" {
		score += 0.2; reasons = append(reasons, "DMARC policy failed")
	}

	action := "pass"
	if score >= 0.7 {
		action = "block"
	} else if score >= 0.4 {
		action = "quarantine"
	}

	return &AnalyzeResult{
		ThreatScore: score,
		Verdict:     "unknown",
		Confidence:  0.5,
		Reasons:     reasons,
		Action:      action,
		Explanation: "Offline analysis based on SPF/DKIM/DMARC results",
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// API provides HTTP handlers for Guardian.
type API struct {
	agent *Agent
}

func NewAPI(agent *Agent) *API {
	return &API{agent: agent}
}

func (api *API) AnalyzeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	defer r.Body.Close()

	var req AnalyzeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	result, err := api.agent.Analyze(r.Context(), &req)
	if err != nil {
		http.Error(w, "analysis failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
