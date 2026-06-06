package firewall

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Pipeline processes inbound emails through multiple security layers.
type Pipeline struct {
	layers []Layer
	logger *zap.Logger
	mu     sync.RWMutex
}

// Layer represents a single security check in the pipeline.
type Layer interface {
	Name() string
	Score(ctx context.Context, email *EmailContext) (float64, string, error)
}

// EmailContext contains metadata about an inbound email.
type EmailContext struct {
	MessageID  string
	SenderIP   string
	SenderDomain string
	Recipient  string
	Subject    string
	Body       string
	HasAttachments bool
	SPFResult  string
	DKIMResult string
	DMARCResult string
	ReceivedAt time.Time
}

// NewPipeline creates a new firewall pipeline.
func NewPipeline(logger *zap.Logger) *Pipeline {
	return &Pipeline{
		logger: logger,
	}
}

// AddLayer adds a security layer to the pipeline.
func (p *Pipeline) AddLayer(layer Layer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.layers = append(p.layers, layer)
	p.logger.Info("firewall layer added", zap.String("layer", layer.Name()))
}

// Process runs the email through all pipeline layers.
func (p *Pipeline) Process(ctx context.Context, email *EmailContext) (*Verdict, error) {
	p.mu.RLock()
	layers := make([]Layer, len(p.layers))
	copy(layers, p.layers)
	p.mu.RUnlock()

	totalScore := 0.0
	var reasons []string
	var action string = "pass"

	for _, layer := range layers {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		score, reason, err := layer.Score(ctx, email)
		if err != nil {
			p.logger.Warn("firewall layer error",
				zap.String("layer", layer.Name()),
				zap.Error(err),
			)
			continue
		}

		totalScore += score
		if reason != "" {
			reasons = append(reasons, reason)
		}
	}

	switch {
	case totalScore >= 8.0:
		action = "block"
	case totalScore >= 5.0:
		action = "quarantine"
	default:
		action = "pass"
	}

	verdict := &Verdict{
		TotalScore: totalScore,
		Action:     action,
		Reasons:    reasons,
	}

	p.logger.Info("firewall verdict",
		zap.Float64("score", totalScore),
		zap.String("action", action),
		zap.Strings("reasons", reasons),
	)

	return verdict, nil
}

// Verdict represents the result of email analysis.
type Verdict struct {
	TotalScore float64  `json:"total_score"`
	Action     string   `json:"action"`
	Reasons    []string `json:"reasons"`
}
