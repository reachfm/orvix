package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type EventType string

const (
	EventDomainCreated   EventType = "domain.created"
	EventDomainDeleted   EventType = "domain.deleted"
	EventUserCreated     EventType = "user.created"
	EventUserDeleted     EventType = "user.deleted"
	EventMailboxCreated  EventType = "mailbox.created"
	EventMailboxDeleted  EventType = "mailbox.deleted"
	EventBounce          EventType = "bounce.detected"
	EventSpamReport      EventType = "spam.reported"
	EventThreatBlocked   EventType = "threat.blocked"
	EventLicenseExpiring EventType = "license.expiring"
	EventUpdateAvailable EventType = "update.available"
)

type Webhook struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"`
	Events    []string  `json:"events"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type WebhookPayload struct {
	Event     EventType   `json:"event"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

type Service struct {
	mu       sync.RWMutex
	webhooks []Webhook
	client   *http.Client
}

func NewService() *Service {
	return &Service{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Service) Register(webhook Webhook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webhooks = append(s.webhooks, webhook)
}

func (s *Service) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []Webhook
	for _, w := range s.webhooks {
		if w.ID != id {
			filtered = append(filtered, w)
		}
	}
	s.webhooks = filtered
}

func (s *Service) List() []Webhook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Webhook, len(s.webhooks))
	copy(result, s.webhooks)
	return result
}

func (s *Service) Dispatch(event EventType, data interface{}) {
	s.mu.RLock()
	targets := make([]Webhook, 0)
	for _, w := range s.webhooks {
		if !w.Active {
			continue
		}
		for _, evt := range w.Events {
			if evt == string(event) || evt == "*" {
				targets = append(targets, w)
				break
			}
		}
	}
	s.mu.RUnlock()

	payload := WebhookPayload{
		Event:     event,
		Timestamp: time.Now().Unix(),
		Data:      data,
	}

	for _, target := range targets {
		go s.send(target, payload)
	}
}

func (s *Service) send(webhook Webhook, payload WebhookPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	signature := SignPayload(body, webhook.Secret)

	req, err := http.NewRequest("POST", webhook.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OrvixEM-Signature-256", signature)
	req.Header.Set("X-OrvixEM-Event", string(payload.Event))
	req.Header.Set("User-Agent", "OrvixEM-Webhook/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func SignPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(body []byte, signature, secret string) bool {
	expected := SignPayload(body, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}
