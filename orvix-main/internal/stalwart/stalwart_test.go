package stalwart

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	c := NewClient("http://localhost:18080", "test-api-key", logger)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.baseURL != "http://localhost:18080" {
		t.Fatalf("expected baseURL http://localhost:18080, got %s", c.baseURL)
	}
	if c.apiKey != "test-api-key" {
		t.Fatalf("expected apiKey test-api-key, got %s", c.apiKey)
	}
}

func TestNewClientDefaultTimeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	c := NewClient("http://localhost:18080", "", logger)
	if c.httpClient.Timeout != defaultTimeout {
		t.Fatalf("expected timeout %v, got %v", defaultTimeout, c.httpClient.Timeout)
	}
}

func TestDomainStruct(t *testing.T) {
	d := Domain{Name: "example.com"}
	if d.Name != "example.com" {
		t.Fatalf("expected domain name example.com, got %s", d.Name)
	}
}

func TestPrincipalStruct(t *testing.T) {
	p := Principal{
		Name:    "user@example.com",
		Type:    "individual",
		Quota:   1073741824,
		Emails:  []string{"user@example.com"},
		Enabled: true,
	}
	if p.Name != "user@example.com" {
		t.Fatalf("unexpected principal name: %s", p.Name)
	}
	if !p.Enabled {
		t.Fatal("expected enabled principal")
	}
	if len(p.Emails) != 1 || p.Emails[0] != "user@example.com" {
		t.Fatal("unexpected emails")
	}
}

func TestQueueMessageStruct(t *testing.T) {
	now := time.Now()
	qm := QueueMessage{
		ID:        "msg-123",
		From:      "sender@example.com",
		To:        []string{"recipient@example.com"},
		Size:      4096,
		Status:    "queued",
		CreatedAt: now,
	}
	if qm.ID != "msg-123" {
		t.Fatalf("unexpected queue message ID: %s", qm.ID)
	}
	if qm.Status != "queued" {
		t.Fatalf("unexpected status: %s", qm.Status)
	}
	if qm.Size != 4096 {
		t.Fatalf("unexpected size: %d", qm.Size)
	}
	if len(qm.To) != 1 {
		t.Fatalf("unexpected recipients count: %d", len(qm.To))
	}
}

func TestConfigGenerator(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cg := NewConfigGenerator("/tmp/data", "/tmp/config", "/tmp/log", "key123", logger)
	if cg == nil {
		t.Fatal("NewConfigGenerator returned nil")
	}
}

func TestNewEventHandler(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	eh := NewEventHandler(logger)
	if eh == nil {
		t.Fatal("NewEventHandler returned nil")
	}
}

func TestEventHandlerRegisterHandler(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	eh := NewEventHandler(logger)

	called := false
	eh.RegisterHandler(EventEmailReceived, func(event WebhookEvent) error {
		called = true
		return nil
	})

	if len(eh.handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(eh.handlers))
	}

	handler, ok := eh.handlers[EventEmailReceived]
	if !ok {
		t.Fatal("handler not registered for EMAIL_RECEIVED")
	}

	handler(WebhookEvent{Type: EventEmailReceived})
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestNewProcessManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	pm := NewProcessManager("/usr/bin/stalwart", "/tmp/data", "/tmp/config", "/tmp/log", logger)
	if pm == nil {
		t.Fatal("NewProcessManager returned nil")
	}
	if pm.binPath != "/usr/bin/stalwart" {
		t.Fatalf("unexpected binPath: %s", pm.binPath)
	}
}

func TestProcessManagerIsNotRunningInitially(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	pm := NewProcessManager("", "", "", "", logger)
	if pm.IsRunning() {
		t.Fatal("process should not be running initially")
	}
}
