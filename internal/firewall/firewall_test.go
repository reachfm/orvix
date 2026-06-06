package firewall

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

type testLayer struct {
	name  string
	score float64
	reason string
	err   error
}

func (l *testLayer) Name() string { return l.name }
func (l *testLayer) Score(ctx context.Context, email *EmailContext) (float64, string, error) {
	return l.score, l.reason, l.err
}

func TestPipelineProcess_Pass(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "test-layer", score: 1.0, reason: "low risk"})

	email := &EmailContext{
		MessageID:  "test-123",
		SenderIP:   "1.2.3.4",
		Subject:    "Hello",
		ReceivedAt: time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if verdict.Action != "pass" {
		t.Fatalf("expected action=pass, got %s", verdict.Action)
	}
}

func TestPipelineProcess_Block(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "high-risk-layer", score: 9.0, reason: "known spammer IP"})

	email := &EmailContext{
		MessageID:  "test-456",
		SenderIP:   "10.0.0.1",
		ReceivedAt: time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if verdict.Action != "block" {
		t.Fatalf("expected action=block, got %s", verdict.Action)
	}
	if verdict.TotalScore != 9.0 {
		t.Fatalf("expected total score 9.0, got %f", verdict.TotalScore)
	}
}

func TestPipelineProcess_Quarantine(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "suspicious-layer", score: 5.5, reason: "suspicious content"})

	email := &EmailContext{
		MessageID:  "test-789",
		SenderIP:   "10.0.0.2",
		ReceivedAt: time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if verdict.Action != "quarantine" {
		t.Fatalf("expected action=quarantine, got %s", verdict.Action)
	}
}

func TestPipelineMultipleLayers(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "layer-1", score: 2.0, reason: "low IP rep"})
	p.AddLayer(&testLayer{name: "layer-2", score: 3.0, reason: "failed SPF"})
	p.AddLayer(&testLayer{name: "layer-3", score: 1.0, reason: "suspicious link"})

	email := &EmailContext{
		MessageID:   "test-multi",
		SenderIP:    "10.0.0.3",
		SenderDomain: "spam.example.com",
		SPFResult:   "fail",
		ReceivedAt:  time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if verdict.TotalScore != 6.0 {
		t.Fatalf("expected total score 6.0, got %f", verdict.TotalScore)
	}
	if verdict.Action != "quarantine" {
		t.Fatalf("expected action=quarantine, got %s", verdict.Action)
	}
	if len(verdict.Reasons) != 3 {
		t.Fatalf("expected 3 reasons, got %d", len(verdict.Reasons))
	}
}

func TestPipelineCancellation(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "slow-layer", score: 9.0, reason: "should not run"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	email := &EmailContext{
		MessageID:  "test-cancel",
		SenderIP:   "1.2.3.4",
		ReceivedAt: time.Now(),
	}

	_, err := p.Process(ctx, email)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestPipelineEmpty(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	email := &EmailContext{
		MessageID:  "test-empty",
		SenderIP:   "1.2.3.4",
		ReceivedAt: time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if verdict.Action != "pass" {
		t.Fatalf("expected action=pass with no layers, got %s", verdict.Action)
	}
	if verdict.TotalScore != 0.0 {
		t.Fatalf("expected score 0.0 with no layers, got %f", verdict.TotalScore)
	}
}

func TestPipelineLayerError(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewPipeline(logger)

	p.AddLayer(&testLayer{name: "error-layer", score: 0, reason: "", err: nil})

	email := &EmailContext{
		MessageID:  "test-error",
		SenderIP:   "1.2.3.4",
		ReceivedAt: time.Now(),
	}

	verdict, err := p.Process(context.Background(), email)
	if err != nil {
		t.Fatalf("Process with layer error should not fail: %v", err)
	}
	if verdict.TotalScore != 0.0 {
		t.Fatalf("expected score 0.0 after layer error, got %f", verdict.TotalScore)
	}
}

func TestEmailContext(t *testing.T) {
	now := time.Now()
	email := &EmailContext{
		MessageID:      "test-ctx-1",
		SenderIP:       "192.168.1.1",
		SenderDomain:   "example.com",
		Recipient:      "user@orvix.email",
		Subject:        "Test Email",
		Body:           "Hello World",
		HasAttachments: true,
		SPFResult:      "pass",
		DKIMResult:     "pass",
		DMARCResult:    "pass",
		ReceivedAt:     now,
	}

	if email.MessageID != "test-ctx-1" {
		t.Fatalf("unexpected MessageID: %s", email.MessageID)
	}
	if !email.HasAttachments {
		t.Fatal("expected HasAttachments=true")
	}
	if email.ReceivedAt != now {
		t.Fatal("unexpected ReceivedAt")
	}
}
