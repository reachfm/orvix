package stalwart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultTimeout = 30 * time.Second
	maxRetries     = 3
	backoffBase    = 1 * time.Second
)

// Client communicates with the Stalwart REST API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a new Stalwart API client.
func NewClient(baseURL, apiKey string, logger *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				MaxIdleConns:    20,
				IdleConnTimeout: 90 * time.Second,
			},
		},
		logger: logger,
	}
}

// Domain represents a mail domain in Stalwart.
type Domain struct {
	Name string `json:"name"`
}

// Principal represents a mailbox/user in Stalwart.
type Principal struct {
	ID       uint64 `json:"id,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Quota    int64  `json:"quota,omitempty"`
	DomainID uint64 `json:"domainId,omitempty"`
	Emails   []string `json:"emails,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// CreateDomain adds a domain to Stalwart.
func (c *Client) CreateDomain(ctx context.Context, name string) error {
	domain := Domain{Name: name}
	return c.doJSON(ctx, http.MethodPost, "/api/domain", domain, nil)
}

// DeleteDomain removes a domain from Stalwart.
func (c *Client) DeleteDomain(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/domain/%s", name), nil, nil)
}

// ListDomains returns all domains from Stalwart.
func (c *Client) ListDomains(ctx context.Context) ([]Domain, error) {
	var domains []Domain
	err := c.doJSON(ctx, http.MethodGet, "/api/domain", nil, &domains)
	return domains, err
}

// CreatePrincipal creates a mailbox/user in Stalwart.
func (c *Client) CreatePrincipal(ctx context.Context, principal Principal) error {
	return c.doJSON(ctx, http.MethodPost, "/api/principal", principal, nil)
}

// DeletePrincipal removes a mailbox/user from Stalwart.
func (c *Client) DeletePrincipal(ctx context.Context, id uint64) error {
	return c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/principal/%d", id), nil, nil)
}

// ListPrincipals returns all principals from Stalwart.
func (c *Client) ListPrincipals(ctx context.Context) ([]Principal, error) {
	var principals []Principal
	err := c.doJSON(ctx, http.MethodGet, "/api/principal", nil, &principals)
	return principals, err
}

// QueueMessage represents a message in the Stalwart mail queue.
type QueueMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Size      int64     `json:"size"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// ListQueue returns messages in the mail queue.
func (c *Client) ListQueue(ctx context.Context) ([]QueueMessage, error) {
	var messages []QueueMessage
	err := c.doJSON(ctx, http.MethodGet, "/api/queue/messages", nil, &messages)
	return messages, err
}

// DeleteQueueMessage removes a message from the queue.
func (c *Client) DeleteQueueMessage(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/queue/messages/%s", id), nil, nil)
}

// RetryQueueMessage forces a retry for a queued message.
func (c *Client) RetryQueueMessage(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/queue/messages/%s/retry", id), nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := c.baseURL + path

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoffBase * time.Duration(1<<attempt))
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("stalwart request failed: %w", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			lastErr = fmt.Errorf("stalwart API error (status %d): %s", resp.StatusCode, string(respBody))
			continue
		}

		if result != nil {
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}
		}

		return nil
	}

	return fmt.Errorf("stalwart request failed after %d retries: %w", maxRetries, lastErr)
}
