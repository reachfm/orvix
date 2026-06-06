package guardian

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewAgent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	a := NewAgent("test-key", "deepseek-chat", logger)
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	if a.apiKey != "test-key" {
		t.Fatalf("expected apiKey 'test-key', got %s", a.apiKey)
	}
}

func TestOfflineAnalysis_Pass(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	a := NewAgent("", "deepseek-chat", logger)

	result := a.offlineAnalysis(&AnalyzeRequest{
		EmailID: "test-1", SenderIP: "1.2.3.4",
		SPFResult: "pass", DKIMResult: "pass", DMARCResult: "pass",
	})

	if result.Action != "pass" {
		t.Fatalf("expected action=pass, got %s", result.Action)
	}
}

func TestOfflineAnalysis_Block(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	a := NewAgent("", "deepseek-chat", logger)

	result := a.offlineAnalysis(&AnalyzeRequest{
		EmailID: "test-2", SenderIP: "10.0.0.1",
		SPFResult: "fail", DKIMResult: "fail", DMARCResult: "fail",
	})

	if result.Action != "block" {
		t.Fatalf("expected action=block, got %s", result.Action)
	}
	if result.ThreatScore < 0.7 {
		t.Fatalf("expected threat score >= 0.7, got %f", result.ThreatScore)
	}
}

func TestOfflineAnalysis_Quarantine(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	a := NewAgent("", "deepseek-chat", logger)

	result := a.offlineAnalysis(&AnalyzeRequest{
		EmailID: "test-3", SenderIP: "10.0.0.2",
		SPFResult: "fail", DKIMResult: "fail", DMARCResult: "pass",
	})

	if result.Action != "quarantine" {
		t.Fatalf("expected action=quarantine, got %s (score=%f)", result.Action, result.ThreatScore)
	}
}

func TestAnalyzeRequest(t *testing.T) {
	req := &AnalyzeRequest{
		EmailID: "test-456", SenderIP: "192.168.1.1",
		SenderDomain: "example.com", Subject: "Test",
		HasAttachments: true, SPFResult: "pass",
	}
	if req.EmailID != "test-456" {
		t.Fatalf("unexpected EmailID: %s", req.EmailID)
	}
	if !req.HasAttachments {
		t.Fatal("expected HasAttachments=true")
	}
}

func TestAnalyzeResult(t *testing.T) {
	r := &AnalyzeResult{
		ThreatScore: 0.85, Verdict: "phishing",
		Confidence: 0.95, Action: "block",
		Reasons: []string{"Suspicious link", "New domain"},
	}
	if r.ThreatScore != 0.85 {
		t.Fatalf("unexpected score: %f", r.ThreatScore)
	}
	if len(r.Reasons) != 2 {
		t.Fatalf("expected 2 reasons, got %d", len(r.Reasons))
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Fatal("should not truncate short string")
	}
	if truncate("hello world", 5) != "hello" {
		t.Fatal("should truncate to 5 chars")
	}
}

func TestGuardianLog(t *testing.T) {
	log := GuardianLog{
		MessageID: "msg-001", ThreatScore: 0.9,
		Verdict: "malware", Action: "block",
	}
	if log.MessageID != "msg-001" {
		t.Fatalf("unexpected MessageID: %s", log.MessageID)
	}
}
