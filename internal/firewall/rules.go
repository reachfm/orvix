package firewall

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	maxRuleLength    = 2048
	regexTimeout     = 1 * time.Second
)

// RuleEngine evaluates user-defined firewall rules.
type RuleEngine struct {
	db     *gorm.DB
	logger *zap.Logger
	mu     sync.RWMutex
	rules  []models.FirewallRule
}

// NewRuleEngine creates a new rule engine.
func NewRuleEngine(db *gorm.DB, logger *zap.Logger) *RuleEngine {
	return &RuleEngine{
		db:     db,
		logger: logger,
	}
}

// LoadRules loads active rules from the database.
func (re *RuleEngine) LoadRules() error {
	re.mu.Lock()
	defer re.mu.Unlock()

	var rules []models.FirewallRule
	if err := re.db.Where("enabled = ?", true).Order("priority asc").Find(&rules).Error; err != nil {
		return fmt.Errorf("failed to load firewall rules: %w", err)
	}

	re.rules = rules
	re.logger.Info("firewall rules loaded", zap.Int("count", len(rules)))
	return nil
}

// Evaluate checks an email against all rules.
func (re *RuleEngine) Evaluate(ctx context.Context, email *EmailContext) (string, string, error) {
	re.mu.RLock()
	rules := make([]models.FirewallRule, len(re.rules))
	copy(rules, re.rules)
	re.mu.RUnlock()

	for _, rule := range rules {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		matched, err := re.matchCondition(rule.Condition, email)
		if err != nil {
			continue
		}

		if matched {
			re.logger.Info("firewall rule matched",
				zap.String("rule", rule.Name),
				zap.String("action", rule.Action),
			)
			return rule.Action, fmt.Sprintf("matched rule: %s", rule.Name), nil
		}
	}

	return "pass", "", nil
}

func (re *RuleEngine) matchCondition(condition string, email *EmailContext) (bool, error) {
	if len(condition) > maxRuleLength {
		return false, fmt.Errorf("rule condition exceeds maximum length of %d", maxRuleLength)
	}

	resolved := condition
	resolved = replaceAll(resolved, "{sender_domain}", regexp.QuoteMeta(email.SenderDomain))
	resolved = replaceAll(resolved, "{sender_ip}", regexp.QuoteMeta(email.SenderIP))
	resolved = replaceAll(resolved, "{recipient}", regexp.QuoteMeta(email.Recipient))
	resolved = replaceAll(resolved, "{subject}", regexp.QuoteMeta(email.Subject))

	compiled, err := regexp.Compile(resolved)
	if err != nil {
		return false, fmt.Errorf("invalid rule condition regex: %w", err)
	}

	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("regex panic: %v", r)
			}
		}()
		matched := compiled.MatchString(condition)
		resultCh <- matched
	}()

	select {
	case matched := <-resultCh:
		return matched, nil
	case err := <-errCh:
		return false, err
	case <-time.After(regexTimeout):
		return false, fmt.Errorf("rule condition evaluation timed out after %v", regexTimeout)
	}
}

func replaceAll(s, key, val string) string {
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if i+len(key) <= len(s) && s[i:i+len(key)] == key {
			result = append(result, []byte(val)...)
			i += len(key)
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}
