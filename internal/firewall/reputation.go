package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// IPReputation checks sender IP reputation via AbuseIPDB.
type IPReputation struct {
	apiKey string
	client *http.Client
	cache  map[string]cachedResult
	mu     sync.RWMutex
	logger *zap.Logger
}

type cachedResult struct {
	score   float64
	expires time.Time
}

// AbuseIPDBResponse represents the AbuseIPDB API response.
type AbuseIPDBResponse struct {
	Data struct {
		AbuseConfidenceScore int    `json:"abuseConfidenceScore"`
		TotalReports         int    `json:"totalReports"`
		ISP                  string `json:"isp"`
		Domain               string `json:"domain"`
		UsageType            string `json:"usageType"`
	} `json:"data"`
}

// NewIPReputation creates a new IP reputation checker.
func NewIPReputation(apiKey string, logger *zap.Logger) *IPReputation {
	return &IPReputation{
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
		cache:  make(map[string]cachedResult),
		logger: logger,
	}
}

// Check returns an abuse score (0-100) for the given IP.
func (ir *IPReputation) Check(ctx context.Context, ip string) (float64, error) {
	ir.mu.RLock()
	if cached, ok := ir.cache[ip]; ok && time.Now().Before(cached.expires) {
		ir.mu.RUnlock()
		return cached.score, nil
	}
	ir.mu.RUnlock()

	if ir.apiKey == "" {
		return 0, nil
	}

	url := fmt.Sprintf("https://api.abuseipdb.com/api/v2/check?ip=%s&maxAgeInDays=90", ip)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Key", ir.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := ir.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("abuseipdb request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var result AbuseIPDBResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	score := float64(result.Data.AbuseConfidenceScore) / 100.0

	ir.mu.Lock()
	ir.cache[ip] = cachedResult{score: score, expires: time.Now().Add(15 * time.Minute)}
	ir.mu.Unlock()

	return score, nil
}
