package licensingauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxRetries       = 3
	baseRetryDelay   = 500 * time.Millisecond
	circuitThreshold = 5
	circuitCooldown  = 60 * time.Second
)

type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

type HTTPAuthorityClient struct {
	baseURL     string
	httpClient  *http.Client
	testMode    bool
	mu          sync.Mutex
	failures    int
	state       circuitState
	lastFailure time.Time
}

type HTTPAuthorityConfig struct {
	BaseURL  string
	Timeout  time.Duration
	TestMode bool
}

func NewHTTPAuthorityClient(cfg HTTPAuthorityConfig) (*HTTPAuthorityClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	if !cfg.TestMode && !strings.HasPrefix(cfg.BaseURL, "https://") {
		return nil, fmt.Errorf("HTTPS required unless test mode is enabled")
	}

	if cfg.TestMode && strings.HasPrefix(cfg.BaseURL, "http://") {
		// Allow HTTP in test mode
	} else if !strings.HasPrefix(cfg.BaseURL, "https://") && !strings.HasPrefix(cfg.BaseURL, "http://") {
		return nil, fmt.Errorf("invalid URL scheme: must be http:// or https://")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &HTTPAuthorityClient{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		testMode: cfg.TestMode,
		state:    circuitClosed,
	}, nil
}

func (c *HTTPAuthorityClient) Validate(ctx context.Context, req *ValidationRequest) (*ValidationResponse, error) {
	var resp ValidationResponse
	err := c.doRequest(ctx, "POST", "/validate", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPAuthorityClient) Activate(ctx context.Context, req *ActivationRequest) (*ActivationResponse, error) {
	var resp ActivationResponse
	err := c.doRequest(ctx, "POST", "/activate", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPAuthorityClient) Heartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	err := c.doRequest(ctx, "POST", "/heartbeat", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPAuthorityClient) Entitlements(ctx context.Context, req *EntitlementRequest) (*EntitlementResponse, error) {
	var resp EntitlementResponse
	err := c.doRequest(ctx, "POST", "/entitlements", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *HTTPAuthorityClient) doRequest(ctx context.Context, method, path string, reqBody, respBody interface{}) error {
	if err := c.checkCircuit(); err != nil {
		return err
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = c.redactError(err)
			continue
		}

		respData, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, c.redactString(string(respData)))
			if resp.StatusCode >= 500 {
				continue
			}
			c.recordFailure()
			return lastErr
		}

		if err := json.Unmarshal(respData, respBody); err != nil {
			c.recordFailure()
			return fmt.Errorf("invalid JSON response: %w", err)
		}

		c.recordSuccess()
		return nil
	}

	c.recordFailure()
	return fmt.Errorf("request failed after %d retries: %w", maxRetries, lastErr)
}

func (c *HTTPAuthorityClient) checkCircuit() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case circuitOpen:
		if time.Since(c.lastFailure) < circuitCooldown {
			return fmt.Errorf("circuit breaker open")
		}
		c.state = circuitHalfOpen
		return nil
	case circuitHalfOpen, circuitClosed:
		return nil
	}
	return nil
}

func (c *HTTPAuthorityClient) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	c.state = circuitClosed
}

func (c *HTTPAuthorityClient) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	c.lastFailure = time.Now()
	if c.failures >= circuitThreshold {
		c.state = circuitOpen
	}
}

func (c *HTTPAuthorityClient) redactError(err error) error {
	msg := err.Error()
	return fmt.Errorf("%s", c.redactString(msg))
}

func (c *HTTPAuthorityClient) redactString(s string) string {
	s = strings.ReplaceAll(s, "licenseId", "[REDACTED]")
	s = strings.ReplaceAll(s, "machineId", "[REDACTED]")
	return s
}
